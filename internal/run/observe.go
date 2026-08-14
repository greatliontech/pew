package run

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/greatliontech/gofresh/runtimeinput"
	"github.com/greatliontech/pew/internal/gotool"
)

// CaptureObservationFrame captures the pre-spawn half of the
// completed-observation conjunction (spec §7.8) through gofresh's
// producer facade: resolved tree roots and the observation bracket
// fingerprinted over the package directory (VCS bookkeeping excluded)
// immediately before the measurement invocation. Frame failures never
// abort the run: a refused frame carries its reason and the run's
// observation records incomplete. (The classification-root read is
// different: a broken `go env` breaks the measurement itself, so it
// aborts like every other toolchain probe.)
func CaptureObservationFrame(ctx context.Context, moduleDir, pkgRel string) runtimeinput.ProducerFrame {
	return runtimeinput.CaptureProducerFrame(ctx, moduleDir,
		filepath.Join(moduleDir, filepath.FromSlash(pkgRel)), runtimeinput.FrameOptions{})
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
	resolved := gotool.CommandDir(moduleDir)
	cmd.Dir = resolved
	cmd.Env = gotool.CommandEnvironment(env, resolved)
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
// capture and the pre-spawn frame through the facade's fold discipline
// (spec §7.8): a refused frame, a missing, unreadable, or never-opened
// capture, and any ingest failure each record the canonical incomplete
// disposition with the honest reason — never an absent manifest, which
// would assert "no runtime inputs" and serve. Spawn and ingestion use
// the same environment source: Execute pins the resolved working
// directory by construction, so the go driver hands the test binary
// exactly frame.PkgDir as its PWD and the ingest mirror pins the same
// value — the posture the facade requires. scratch carries the
// package's declared run-scratch name patterns (the //pew:scratch
// directive): each becomes a gofresh scratch namespace over the
// package directory, admitting recordless only reads the engine proves
// absent at both bracket endpoints — the declaration forfeits exactly
// the appearance-pin of absence-probes matching the pattern, the
// caller-side responsibility the directive's author takes on.
func IngestObservation(ctx context.Context, frame runtimeinput.ProducerFrame, logPath, identity string, env []string, roots GoEnvRoots, scratch ...string) (runtimeinput.State, error) {
	namespaces := make([]runtimeinput.ScratchNamespace, 0, len(scratch))
	for _, pattern := range scratch {
		namespaces = append(namespaces, runtimeinput.ScratchNamespace{Dir: frame.PkgRel, Pattern: pattern})
	}
	observation, _, err := frame.Observe(ctx, logPath, runtimeinput.ProducerIngest{
		Identity: identity,
		Env:      gotool.CommandEnvironment(env, frame.PkgDir),
		Roots: runtimeinput.ClassificationRoots{
			Toolchain:     roots.Toolchain,
			ModuleCache:   roots.ModuleCache,
			BuildCache:    roots.BuildCache,
			EphemeralTemp: filepath.Clean(os.TempDir()),
		},
		ScratchNamespaces: namespaces,
	})
	if err != nil {
		return runtimeinput.State{}, err
	}
	return runtimeinput.CompletedState(observation)
}
