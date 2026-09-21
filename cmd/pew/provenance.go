package main

import (
	"context"
	"errors"
	"os/exec"

	"github.com/greatliontech/gofresh"
	gofreshtool "github.com/greatliontech/gofresh/gotool"
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
// half. Swapped only by tests. The default is gofresh's memoized
// sampler (gotool.Sampler: one sample per coordinate and normalized
// environment for the process, a failed sample memoized like an
// answered one, a cancelled sample never memoized) under pew's policy,
// so `go env` exec cost stays constant in package count and an alias
// and its target are one key.
var goVersionSampler = sampleGoVersion

// toolchainSampler is the process's one memo: gofresh's sampler over
// pew's runner, whose boundary hook is the seam a pin observes the
// spawn's directory and environment through (goVersionSampler above
// swaps the whole sample for the skew fixtures — two seams for two
// questions). The hook reads sampleCommandObserver at spawn time.
var toolchainSampler = &gofreshtool.Sampler{Runner: gotool.Runner(observeSample)}

func observeSample(cmd *exec.Cmd) {
	if sampleCommandObserver != nil {
		sampleCommandObserver(cmd)
	}
}

// sampleGoVersion is the toolchain sample through pew's one go-command
// policy: the directory resolved and a nil environment inherited (an
// environment the policy refuses is pew's own class), then gofresh's
// memoized sampler — the sample resolves exactly as the engine's own
// loads do, the module's go.mod toolchain directive included, under
// the effective environment.
func sampleGoVersion(ctx context.Context, dir string, env []string) (string, error) {
	resolved, env, err := gotool.Resolve(dir, env)
	if err != nil {
		return "", err
	}
	return toolchainSampler.Sample(ctx, resolved, env)
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
	// The composite is gofresh's: the memoized sample, then the skew
	// judgment, both refusals one typed class whose message already
	// names what this side could read beside a failed sample (spec §7's
	// refusal contract); the memo lives in toolchainSampler, the
	// package's one, so a composite per check costs nothing.
	_, err := (&gofresh.ToolchainProvenance{Sampler: gofresh.SampleFunc(goVersionSampler)}).Check(ctx, dir, env)
	var envErr *gotool.EnvironmentError
	if errors.As(err, &envErr) {
		// The caller's environment refused by the go-command policy
		// says nothing about the toolchain: its own class, unwrapped
		// from the composite's refusal, so the operator reads the
		// environment fault, not a rebuild.
		return envErr
	}
	var pe *gofresh.ToolchainProvenanceError
	if errors.As(err, &pe) {
		return &toolchainProvenanceError{err: pe.Err}
	}
	return err
}
