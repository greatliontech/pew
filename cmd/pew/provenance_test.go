package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	goVersionSampler = func(dir string, env []string) (string, error) {
		sampledDir = dir
		sampledEnv = env
		return "go99.1.0", nil
	}
	dir := t.TempDir()
	env := append(os.Environ(), "PEW_PROVENANCE_PROBE=1")
	if _, err := buildEngine(dir, env, ""); err == nil {
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
	goVersionSampler = func(dir string, env []string) (string, error) {
		return "devel +abc123", nil
	}
	if _, err := buildEngine(t.TempDir(), os.Environ(), ""); err == nil {
		t.Fatal("buildEngine accepted an unidentifiable ambient toolchain")
	} else if !strings.Contains(err.Error(), "unidentifiable") {
		t.Fatalf("refusal = %v, want the unidentifiable class named", err)
	}
}

// goVersionCmd wires the module dir and effective env into the sample.
func TestGoVersionCmdWiresDirAndEnv(t *testing.T) {
	env := []string{"A=1", "B=2"}
	cmd := goVersionCmd("/target/module", env)
	if cmd.Dir != "/target/module" {
		t.Fatalf("cmd.Dir = %q", cmd.Dir)
	}
	if len(cmd.Env) != 2 || cmd.Env[0] != "A=1" {
		t.Fatalf("cmd.Env = %v", cmd.Env)
	}
	if empty := goVersionCmd("/target/module", nil); empty.Env != nil {
		t.Fatalf("nil env must inherit the process environment, got %v", empty.Env)
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

	goVersionSampler = func(dir string, env []string) (string, error) {
		return "go99.1.0", nil
	}
	var pe *toolchainProvenanceError
	if err := checkToolchainProvenance(t.TempDir(), os.Environ()); !errors.As(err, &pe) {
		t.Fatalf("skew refusal %v is not a *toolchainProvenanceError", err)
	}

	goVersionSampler = func(dir string, env []string) (string, error) {
		return "", fmt.Errorf("boom")
	}
	pe = nil
	if err := checkToolchainProvenance(t.TempDir(), os.Environ()); !errors.As(err, &pe) {
		t.Fatalf("sample-failure refusal %v is not a *toolchainProvenanceError", err)
	}
}

// The default sampler memoizes per (dir, env): one `go env` exec per
// distinct key per process, so the prerequisite's cost stays constant
// in package count.
func TestMemoizedSamplerSamplesOncePerKey(t *testing.T) {
	calls := map[string]int{}
	sampler := memoizedSampler(func(dir string, env []string) (string, error) {
		calls[dir]++
		if dir == "/bad" {
			return "", fmt.Errorf("boom")
		}
		return "go1.27.0", nil
	})
	for range 3 {
		if v, err := sampler("/a", []string{"K=1"}); err != nil || v != "go1.27.0" {
			t.Fatalf("sampler(/a) = %q, %v", v, err)
		}
		if _, err := sampler("/bad", []string{"K=1"}); err == nil {
			t.Fatal("memoized failure did not stay a failure")
		}
	}
	if _, err := sampler("/a", []string{"K=2"}); err != nil {
		t.Fatal(err)
	}
	if calls["/a"] != 2 || calls["/bad"] != 1 {
		t.Fatalf("underlying sample calls = %v, want /a:2 (two env keys), /bad:1", calls)
	}
}

// The real sampler resolves an actual GOVERSION in this repo — the
// smoke check that the exec path (command, dir, trimming) works
// outside the swapped-sampler tests.
func TestSampleGoVersionSmoke(t *testing.T) {
	v, err := sampleGoVersion(".", nil)
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
	goVersionSampler = func(dir string, env []string) (string, error) {
		return "go99.1.0", nil
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/skewfast\n\ngo 1.26.4\n")
	writeFile(t, filepath.Join(dir, "bench_test.go"), "package skewfast\n\nimport \"testing\"\n\nfunc BenchmarkNop(b *testing.B) { for range b.N {} }\n")
	withWorkingDir(t, dir)

	var out strings.Builder
	err := runStatus(&out, "", "", false, false, false, []string{"."})
	if err == nil || !strings.Contains(err.Error(), "cross-major") {
		t.Fatalf("runStatus under skew = %v\noutput:\n%s\nwant the invocation-level refusal", err, out.String())
	}
	if strings.Contains(out.String(), "error") {
		t.Fatalf("runStatus under skew degraded to per-package rows:\n%s", out.String())
	}

	var runOut, runErrOut bytes.Buffer
	err = runRun(&runOut, &runErrOut, runConfig{opts: runpkg.Options{Count: 1, Benchtime: "1x", Bench: "."}}, []string{"."})
	if err == nil || !strings.Contains(err.Error(), "cross-major") {
		t.Fatalf("runRun under skew = %v\noutput:\n%s\nwant the invocation-level refusal", err, runOut.String())
	}
	if strings.Contains(runOut.String(), "package(s) failed") || strings.Contains(runOut.String(), "error") {
		t.Fatalf("runRun under skew degraded to per-package rows:\n%s", runOut.String())
	}
}
