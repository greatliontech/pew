package main

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"sync"

	"github.com/greatliontech/gofresh"
)

// toolchainProvenanceError marks the refusal class of the engine's
// toolchain-provenance prerequisite (spec §7): the invocation-level
// abort, distinct from a per-package failure — a skewed or
// unidentifiable frontend would misread every package's sources, so
// no surface degrades package by package on it.
type toolchainProvenanceError struct{ err error }

func (e *toolchainProvenanceError) Error() string { return e.err.Error() }
func (e *toolchainProvenanceError) Unwrap() error { return e.err }

// goVersionSampler reports the ambient toolchain's GOVERSION as the
// target module resolves it — the engine's build-toolchain provenance
// half. Swapped only by tests. The default samples each distinct
// (dir, env) once per process: `go env` exec cost stays constant in
// package count, and within one invocation the sample cannot move
// (the module directive and environment are fixed inputs).
var goVersionSampler = memoizedSampler(sampleGoVersion)

func memoizedSampler(sample func(dir string, env []string) (string, error)) func(dir string, env []string) (string, error) {
	type result struct {
		version string
		err     error
	}
	var mu sync.Mutex
	memo := map[string]result{}
	return func(dir string, env []string) (string, error) {
		key := dir + "\x00" + strings.Join(env, "\x00")
		mu.Lock()
		got, ok := memo[key]
		mu.Unlock()
		if !ok {
			got.version, got.err = sample(dir, env)
			mu.Lock()
			memo[key] = got
			mu.Unlock()
		}
		return got.version, got.err
	}
}

func sampleGoVersion(dir string, env []string) (string, error) {
	out, err := goVersionCmd(dir, env).Output()
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok && len(ee.Stderr) > 0 {
			return "", fmt.Errorf("go env GOVERSION: %v: %s", err, strings.TrimSpace(string(ee.Stderr)))
		}
		return "", fmt.Errorf("go env GOVERSION: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// goVersionCmd is pure construction, split so the Dir/Env wiring is
// unit-pinnable: the sample must resolve exactly as the engine's own
// loads do — the target module's directory (its go.mod toolchain
// directive included) under the effective environment.
func goVersionCmd(dir string, env []string) *exec.Cmd {
	cmd := exec.Command("go", "env", "GOVERSION")
	cmd.Dir = dir
	if len(env) > 0 {
		cmd.Env = env
	}
	return cmd
}

// checkToolchainProvenance refuses the judged-run states where this
// binary's compiled-in analysis frontend cannot faithfully read what
// the ambient toolchain builds (gofresh.ToolchainSkew: directional
// within a major, total across majors) — the guard every engine
// construction inherits, so no verdict is computed over a tree the
// binary misparses (the go1.27 stale-binary episode's structural fix).
func checkToolchainProvenance(dir string, env []string) error {
	ambient, err := goVersionSampler(dir, env)
	if err != nil {
		// A failed sample leaves the ambient side unidentifiable —
		// gofresh's contract refuses that, so the sampling failure is
		// the same invocation-level class as a detected skew. The
		// message names what this side could read (the binary's own
		// build toolchain) and the failing sample (spec §7's refusal
		// message contract).
		return &toolchainProvenanceError{err: fmt.Errorf("toolchain provenance: binary built with %s, ambient toolchain unidentifiable — refusing to judge: %w", runtime.Version(), err)}
	}
	if err := gofresh.ToolchainSkew(ambient); err != nil {
		return &toolchainProvenanceError{err: err}
	}
	return nil
}
