package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"sync"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/pew/internal/gotool"
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
// (the module directive and environment are fixed inputs) — keyed by
// the directory the command resolves to, so an alias and its target
// are one key; a cancelled sample is not memoized, so it never poisons
// a later call.
var goVersionSampler = memoizedSampler(sampleGoVersion)

func memoizedSampler(sample func(ctx context.Context, dir string, env []string) (string, error)) func(ctx context.Context, dir string, env []string) (string, error) {
	type result struct {
		version string
		err     error
	}
	var mu sync.Mutex
	memo := map[string]result{}
	return func(ctx context.Context, dir string, env []string) (string, error) {
		key := gotool.CommandDir(dir) + "\x00" + strings.Join(env, "\x00")
		mu.Lock()
		got, ok := memo[key]
		mu.Unlock()
		if !ok {
			got.version, got.err = sample(ctx, dir, env)
			// A cancellation is not a fact about (dir, env): memoizing
			// it would answer a later live call with a stale
			// "context canceled". Leave the key unset so the next call
			// re-samples.
			if got.err != nil && ctx.Err() != nil {
				return got.version, got.err
			}
			mu.Lock()
			memo[key] = got
			mu.Unlock()
		}
		return got.version, got.err
	}
}

// sampleGoVersion is the toolchain sample through pew's one go-command
// policy (gotool.Sample: gofresh's runner under pew's directory
// resolution and nil-env inheritance) — the sample must resolve exactly
// as the engine's own loads do, the module's go.mod toolchain directive
// included, under the effective environment. The runner's boundary
// hook is the seam a pin observes the spawn's directory and environment
// through; goVersionSampler above swaps the whole sample for the skew
// fixtures — two seams for two questions.
func sampleGoVersion(ctx context.Context, dir string, env []string) (string, error) {
	return gotool.Sample(ctx, dir, env, sampleCommandObserver)
}

// sampleCommandObserver is the runner's boundary hook on the sample's
// command; nil in production, a pin installs one to observe the spawn.
var sampleCommandObserver func(*exec.Cmd)

// checkToolchainProvenance refuses the judged-run states where this
// binary's compiled-in analysis frontend cannot faithfully read what
// the ambient toolchain builds (gofresh.ToolchainSkew: directional
// within a major, total across majors) — the guard every engine
// construction inherits, so no verdict is computed over a tree the
// binary misparses (the go1.27 stale-binary episode's structural fix). An
// environment the go-command policy refuses passes through as its own
// class (gotool.EnvironmentError): a fact about the caller's
// environment, never the toolchain, so it is never the skew refusal.
func checkToolchainProvenance(ctx context.Context, dir string, env []string) error {
	ambient, err := goVersionSampler(ctx, dir, env)
	var envErr *gotool.EnvironmentError
	if errors.As(err, &envErr) {
		// The caller's environment refused by the go-command policy
		// says nothing about the toolchain: its own class, so the
		// operator reads the environment fault, not a rebuild.
		return err
	}
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
