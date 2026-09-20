package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/greatliontech/pew/internal/gotool"
	runpkg "github.com/greatliontech/pew/internal/run"
)

// The engine choke point refuses toolchain-provenance skew before any
// verdict: an ambient toolchain this binary's compiled-in frontend
// cannot faithfully read (newer within the major, or another major)
// must never be judged (gofresh.ToolchainSkew; the go1.27 stale-binary
// episode's structural fix). The sample resolves in the TARGET module
// dir under the effective env — the same resolution the engine's own
// loads use.
func TestBuildEngineRefusesToolchainSkew(t *testing.T) {
	orig := goVersionSampler
	t.Cleanup(func() { goVersionSampler = orig })

	var sampledDir string
	var sampledEnv []string
	goVersionSampler = func(_ context.Context, dir string, env []string) (string, error) {
		sampledDir = dir
		sampledEnv = env
		return "go99.1.0", nil
	}
	dir := t.TempDir()
	env := append(os.Environ(), "PEW_PROVENANCE_PROBE=1")
	if _, err := buildEngine(context.Background(), dir, env, nil, ""); err == nil {
		t.Fatal("buildEngine accepted an ambient toolchain a whole major ahead of the binary")
	} else if !strings.Contains(err.Error(), "cross-major") {
		t.Fatalf("skew refusal = %v, want the cross-major class named", err)
	}
	if sampledDir != dir {
		t.Fatalf("sampled dir = %q, want the target module dir %q", sampledDir, dir)
	}
	probed := false
	for _, kv := range sampledEnv {
		if kv == "PEW_PROVENANCE_PROBE=1" {
			probed = true
		}
	}
	if !probed {
		t.Fatal("the sample did not run under the effective environment")
	}
}

// An unidentifiable ambient toolchain refuses fail-closed.
func TestBuildEngineRefusesUnidentifiableToolchain(t *testing.T) {
	orig := goVersionSampler
	t.Cleanup(func() { goVersionSampler = orig })
	goVersionSampler = func(_ context.Context, dir string, env []string) (string, error) {
		return "devel +abc123", nil
	}
	if _, err := buildEngine(context.Background(), t.TempDir(), os.Environ(), nil, ""); err == nil {
		t.Fatal("buildEngine accepted an unidentifiable ambient toolchain")
	} else if !strings.Contains(err.Error(), "unidentifiable") {
		t.Fatalf("refusal = %v, want the unidentifiable class named", err)
	}
}

// The sample runs under the environment policy: gofresh's runner spawns
// `go env GOVERSION` in the resolved directory with PWD pinned to it and
// the caller's entries kept, observed through the runner's boundary
// hook on a real spawn — a symlinked checkout samples the toolchain
// the engine's own loads read (spec §9's environment policy).
func TestGoVersionSampleRunsUnderTheEnvironmentPolicy(t *testing.T) {
	real := t.TempDir()
	if err := os.WriteFile(filepath.Join(real, "go.mod"), []byte("module example.com/alias\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	// The expectation is the filesystem's own answer, never CommandDir's.
	resolved, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}
	// The caller's environment: the process's, its own PWD replaced by
	// a wrong one the policy must pin over (a duplicated key would be
	// refused by gofresh's normalization, never replaced).
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "PWD=") {
			env = append(env, kv)
		}
	}
	env = append(env, "PEW_SAMPLE_A=1", "PEW_SAMPLE_B=2", "PWD=/somewhere/else")
	var seen *exec.Cmd
	prior := sampleCommandObserver
	sampleCommandObserver = func(cmd *exec.Cmd) { seen = cmd }
	t.Cleanup(func() { sampleCommandObserver = prior })
	version, err := sampleGoVersion(context.Background(), link, env)
	if err != nil || !strings.HasPrefix(version, "go") {
		t.Fatalf("sample = %q, %v", version, err)
	}
	if seen == nil || seen.Dir != resolved {
		t.Fatalf("the sample's Dir = %v, want the resolved dir %q", seen, resolved)
	}
	var pwd string
	for _, kv := range seen.Env {
		if v, ok := strings.CutPrefix(kv, "PWD="); ok {
			pwd = v
		}
	}
	if pwd != resolved {
		t.Fatalf("PWD = %q, want it pinned to the resolved dir %q", pwd, resolved)
	}
	if !slices.Contains(seen.Env, "PEW_SAMPLE_A=1") || !slices.Contains(seen.Env, "PEW_SAMPLE_B=2") {
		t.Fatalf("the sample's env dropped the caller's entries: %v", seen.Env)
	}
	// A nil env inherits THE PROCESS'S environment, as pew's every go
	// invocation does — gofresh's runner alone would refuse it, and an
	// empty one would sample too: the process's marker must reach the
	// spawn.
	t.Setenv("PEW_SAMPLE_MARKER", "inherited")
	seen = nil
	if version, err := sampleGoVersion(context.Background(), link, nil); err != nil || !strings.HasPrefix(version, "go") || seen == nil || !slices.Contains(seen.Env, "PWD="+resolved) || !slices.Contains(seen.Env, "PEW_SAMPLE_MARKER=inherited") {
		t.Fatalf("a nil env did not inherit the process environment: %q, %v, %v", version, err, seen)
	}
	// An environment the go-command policy refuses is its own class
	// through the provenance check — never the toolchain refusal.
	dup := append(slices.Clone(env), "PEW_SAMPLE_A=3")
	err = checkToolchainProvenance(context.Background(), link, dup)
	var envErr *gotool.EnvironmentError
	var pe *toolchainProvenanceError
	if !errors.As(err, &envErr) || errors.As(err, &pe) {
		t.Fatalf("a duplicated key reported as %v; want the environment's own class", err)
	}
}

// The provenance refusal is a distinct error class: run and status
// abort the whole invocation on it (a skewed frontend misreads every
// package) instead of degrading to per-package error rows, and a
// failed sample classifies identically — unidentifiable is not
// agreement.
func TestToolchainProvenanceErrorClassifies(t *testing.T) {
	orig := goVersionSampler
	t.Cleanup(func() { goVersionSampler = orig })

	goVersionSampler = func(_ context.Context, dir string, env []string) (string, error) {
		return "go99.1.0", nil
	}
	var pe *toolchainProvenanceError
	if err := checkToolchainProvenance(context.Background(), t.TempDir(), os.Environ()); !errors.As(err, &pe) {
		t.Fatalf("skew refusal %v is not a *toolchainProvenanceError", err)
	}

	goVersionSampler = func(_ context.Context, dir string, env []string) (string, error) {
		return "", fmt.Errorf("boom")
	}
	pe = nil
	if err := checkToolchainProvenance(context.Background(), t.TempDir(), os.Environ()); !errors.As(err, &pe) {
		t.Fatalf("sample-failure refusal %v is not a *toolchainProvenanceError", err)
	}
}

// The default sampler memoizes per (dir, env): one `go env` exec per
// distinct key per process, so the prerequisite's cost stays constant
// in package count.
func TestMemoizedSamplerSamplesOncePerKey(t *testing.T) {
	calls := map[string]int{}
	sampler := memoizedSampler(func(_ context.Context, dir string, env []string) (string, error) {
		calls[dir]++
		if dir == "/bad" {
			return "", fmt.Errorf("boom")
		}
		return "go1.27.0", nil
	})
	for range 3 {
		if v, err := sampler(context.Background(), "/a", []string{"K=1"}); err != nil || v != "go1.27.0" {
			t.Fatalf("sampler(/a) = %q, %v", v, err)
		}
		if _, err := sampler(context.Background(), "/bad", []string{"K=1"}); err == nil {
			t.Fatal("memoized failure did not stay a failure")
		}
	}
	if _, err := sampler(context.Background(), "/a", []string{"K=2"}); err != nil {
		t.Fatal(err)
	}
	if calls["/a"] != 2 || calls["/bad"] != 1 {
		t.Fatalf("underlying sample calls = %v, want /a:2 (two env keys), /bad:1", calls)
	}
	// A cancellation is not memoized: it must not answer a later live
	// call for the same key with a stale "context canceled".
	ctxCalls := 0
	poison := memoizedSampler(func(ctx context.Context, dir string, env []string) (string, error) {
		ctxCalls++
		if err := ctx.Err(); err != nil {
			return "", err
		}
		return "go1.27.0", nil
	})
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := poison(cancelled, "/c", nil); err == nil {
		t.Fatal("a cancelled sample returned no error")
	}
	if v, err := poison(context.Background(), "/c", nil); err != nil || v != "go1.27.0" {
		t.Fatalf("a live call after a cancelled one = %q, %v; want the fresh sample (the cancellation poisoned the memo)", v, err)
	}
	if ctxCalls != 2 {
		t.Fatalf("underlying calls after cancel+live = %d, want 2 (the cancelled sample was not memoized)", ctxCalls)
	}
	// An alias and its target are one key: the command resolves the
	// directory, so the memo keys by the resolved one.
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	for _, d := range []string{link, real, link} {
		if _, err := sampler(context.Background(), d, []string{"K=1"}); err != nil {
			t.Fatal(err)
		}
	}
	if calls[link]+calls[real] != 1 {
		t.Fatalf("alias and target sampled %d+%d times; want one sample for the one resolved key", calls[link], calls[real])
	}
}

// The real sampler resolves an actual GOVERSION in this repo — the
// smoke check that the exec path (command, dir, trimming) works
// outside the swapped-sampler tests.
func TestSampleGoVersionSmoke(t *testing.T) {
	v, err := sampleGoVersion(context.Background(), ".", nil)
	if err != nil {
		t.Fatalf("sampleGoVersion: %v", err)
	}
	if !strings.HasPrefix(v, "go") || strings.ContainsAny(v, " \n\t") {
		t.Fatalf("sampled GOVERSION = %q, want a trimmed go version string", v)
	}
}

// Skew aborts run and status at the invocation level: no per-package
// error rows, the refusal is the command's error.
func TestRunAndStatusFailFastOnToolchainSkew(t *testing.T) {
	orig := goVersionSampler
	t.Cleanup(func() { goVersionSampler = orig })
	goVersionSampler = func(_ context.Context, dir string, env []string) (string, error) {
		return "go99.1.0", nil
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/skewfast\n\ngo 1.26.4\n")
	writeFile(t, filepath.Join(dir, "bench_test.go"), "package skewfast\n\nimport \"testing\"\n\nfunc BenchmarkNop(b *testing.B) { for range b.N {} }\n")
	withWorkingDir(t, dir)

	var out strings.Builder
	err := runStatus(context.Background(), &out, "", "", false, false, false, []string{"."})
	if err == nil || !strings.Contains(err.Error(), "cross-major") {
		t.Fatalf("runStatus under skew = %v\noutput:\n%s\nwant the invocation-level refusal", err, out.String())
	}
	if strings.Contains(out.String(), "error") {
		t.Fatalf("runStatus under skew degraded to per-package rows:\n%s", out.String())
	}

	var runOut, runErrOut bytes.Buffer
	err = runRun(context.Background(), &runOut, &runErrOut, runConfig{opts: runpkg.Options{Count: 1, Benchtime: "1x", Bench: "."}}, []string{"."})
	if err == nil || !strings.Contains(err.Error(), "cross-major") {
		t.Fatalf("runRun under skew = %v\noutput:\n%s\nwant the invocation-level refusal", err, runOut.String())
	}
	if strings.Contains(runOut.String(), "package(s) failed") || strings.Contains(runOut.String(), "error") {
		t.Fatalf("runRun under skew degraded to per-package rows:\n%s", runOut.String())
	}
}
