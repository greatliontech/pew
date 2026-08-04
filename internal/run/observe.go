package run

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/greatliontech/gofresh/runtimeinput"
)

// ObservationFrame is the pre-spawn half of the completed-observation
// conjunction (spec §7.8): the resolved tree roots and the observation
// bracket fingerprinted over the package directory before the
// measurement process spawns. A non-empty Reason means no frame could
// be captured and the run's observation falls back to the canonical
// incomplete disposition with that reason.
type ObservationFrame struct {
	Root    string
	PkgDir  string
	PkgRel  string
	Bracket runtimeinput.Bracket
	Reason  string
}

// CaptureObservationFrame fingerprints the observation bracket over the
// package directory (VCS bookkeeping excluded) immediately before the
// measurement invocation. Frame failures never abort the run: the frame
// carries the reason and the observation records incomplete. (The
// classification-root read is different: a broken `go env` breaks the
// measurement itself, so it aborts like every other toolchain probe.)
func CaptureObservationFrame(ctx context.Context, moduleDir, pkgRel string) ObservationFrame {
	root, err := filepath.EvalSymlinks(moduleDir)
	if err != nil {
		return ObservationFrame{Reason: fmt.Sprintf("observation bracket capture failed: %v", err)}
	}
	pkgDir, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(pkgRel)))
	if err != nil {
		return ObservationFrame{Reason: fmt.Sprintf("observation bracket capture failed: %v", err)}
	}
	rel, err := filepath.Rel(root, pkgDir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ObservationFrame{Reason: fmt.Sprintf("package directory %s lies outside the module tree; no observation bracket can cover it", pkgDir)}
	}
	bracket, err := runtimeinput.CaptureBracketContext(ctx, root, []string{filepath.ToSlash(rel)},
		runtimeinput.WithBracketExcludedPaths(".git"))
	if err != nil {
		return ObservationFrame{Reason: fmt.Sprintf("observation bracket capture failed: %v", err)}
	}
	return ObservationFrame{Root: root, PkgDir: pkgDir, PkgRel: filepath.ToSlash(rel), Bracket: bracket}
}

// GoEnvRoots carries the toolchain-mediated classification roots the
// testlog ingest options need: reads under them are guard-covered
// (toolchain pin, version-addressed module immutability, cache
// rederivation) rather than observed inputs.
type GoEnvRoots struct {
	Toolchain   string `json:"GOROOT"`
	ModuleCache string `json:"GOMODCACHE"`
	BuildCache  string `json:"GOCACHE"`
}

// ReadGoEnvRoots reads the classification roots from the same toolchain
// and environment the measurement runs under.
func ReadGoEnvRoots(moduleDir string, env []string) (GoEnvRoots, error) {
	cmd := exec.Command("go", "env", "-json", "GOROOT", "GOMODCACHE", "GOCACHE")
	cmd.Dir = moduleDir
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		return GoEnvRoots{}, fmt.Errorf("go env: %w", err)
	}
	var roots GoEnvRoots
	if err := json.Unmarshal(out, &roots); err != nil {
		return GoEnvRoots{}, fmt.Errorf("go env: %w", err)
	}
	return roots, nil
}

// IngestObservation completes the run's observation from the testlog
// capture and the pre-spawn frame, or falls back to the canonical
// incomplete disposition — a process that dies before harness
// completion, a capture the binary never opened, or a missing frame
// records incomplete with the honest reason; absence would assert "no
// runtime inputs" and serve (spec §7.8). scratch carries the package's
// declared run-scratch name patterns (the //pew:scratch directive):
// each becomes a gofresh scratch namespace over the package directory,
// admitting recordless only reads the engine proves absent at both
// bracket endpoints — the declaration forfeits exactly the
// appearance-pin of absence-probes matching the pattern, the
// caller-side responsibility the directive's author takes on.
func IngestObservation(frame ObservationFrame, logPath, moduleDir, identity string, env []string, roots GoEnvRoots, scratch ...string) (runtimeinput.State, error) {
	incomplete := func(reason string) (runtimeinput.State, error) {
		observation, err := runtimeinput.IncompleteEnv(moduleDir, identity, reason, env)
		if err != nil {
			return runtimeinput.State{}, err
		}
		return runtimeinput.CompletedState(observation)
	}
	if frame.Reason != "" {
		return incomplete(frame.Reason)
	}
	log, err := os.ReadFile(logPath)
	if err != nil {
		return incomplete(fmt.Sprintf("testlog capture unreadable: %v", err))
	}
	// The testing runtime writes this header on opening its capture; its
	// absence proves the process died before the harness initialized —
	// the one shape the conjunction cannot complete.
	if !bytes.HasPrefix(log, []byte("# test log")) {
		return incomplete("testlog capture carries no test-log header; the test binary never opened it")
	}
	// Spawn and ingestion MUST use the same environment: the measurement
	// invocation runs from the frame's resolved root, so the go tool
	// hands the test binary exactly frame.PkgDir as its PWD, and the
	// ingested env pins the same value — a divergent PWD is a
	// process-local input that seals the observation.
	processEnv, err := commandEnvironment(env, frame.PkgDir)
	if err != nil {
		return incomplete(fmt.Sprintf("testlog ingest environment: %v", err))
	}
	opts := []runtimeinput.TestLogOption{
		runtimeinput.WithCompletedProcess(identity),
		runtimeinput.WithBracket(frame.Bracket),
		// The module-root listing and VCS bookkeeping are asserted to be
		// no benchmark's input; the exclusion carries the caller-side
		// soundness responsibility gofresh's exclusion contract assigns.
		runtimeinput.WithExcludedPaths(".", ".git"),
		runtimeinput.WithEphemeralTempRoot(filepath.Clean(os.TempDir())),
	}
	for _, pattern := range scratch {
		opts = append(opts, runtimeinput.WithScratchNamespace(frame.PkgRel, pattern))
	}
	if roots.Toolchain != "" {
		opts = append(opts, runtimeinput.WithToolchainRoot(roots.Toolchain))
	}
	if roots.ModuleCache != "" {
		opts = append(opts, runtimeinput.WithModuleCacheRoot(roots.ModuleCache))
	}
	if roots.BuildCache != "" {
		opts = append(opts, runtimeinput.WithBuildCacheRoot(roots.BuildCache))
	}
	observation, err := runtimeinput.FromTestLogEnv(log, frame.Root, frame.PkgDir, processEnv, opts...)
	if err != nil {
		return incomplete(fmt.Sprintf("testlog ingest failed: %v", err))
	}
	return runtimeinput.CompletedState(observation)
}
