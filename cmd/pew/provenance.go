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
// half. Swapped only by tests. The default samples the memoized
// preparation reader: one snapshot per coordinate and normalized
// environment for the invocation, a failed sample memoized like an
// answered one, a cancelled sample never memoized) under pew's policy,
// so provenance adds no second `go env` to build preparation and an alias
// and its target are one key.
var goVersionSampler = sampleGoVersion

type preparationReaderKey struct{}

// withToolchainSampler owns the preparation readers for one judged invocation.
// Toolchain provenance and build configuration read the same snapshot. Write
// validation constructs a fresh reader instead of serving preparation evidence.
func withToolchainSampler(ctx context.Context) context.Context {
	return context.WithValue(ctx, preparationReaderKey{}, &gofreshtool.RunMemo[*gofreshtool.EnvReader]{})
}

func preparationReader(ctx context.Context, dir string, env gotool.Environment) (*gofreshtool.EnvReader, error) {
	makeReader := func() *gofreshtool.EnvReader { return gotool.Reader(dir, env, observeSample) }
	memo, _ := ctx.Value(preparationReaderKey{}).(*gofreshtool.RunMemo[*gofreshtool.EnvReader])
	if memo == nil {
		return makeReader(), nil
	}
	return memo.Get(gotool.CommandDir(dir), env.Values(), makeReader)
}

func observeSample(cmd *exec.Cmd) {
	if sampleCommandObserver != nil {
		sampleCommandObserver(cmd)
	}
}

// sampleGoVersion reads the toolchain sample through pew's one go-command
// policy and gofresh's snapshot sampler — the sample resolves as the engine's own
// loads do, the module's go.mod toolchain directive included, under
// the effective environment.
func sampleGoVersion(ctx context.Context, dir string, env gotool.Environment) (string, error) {
	reader, err := preparationReader(ctx, dir, env)
	if err != nil {
		return "", err
	}
	return (gofreshtool.SnapshotSampler{Reader: reader}).Sample(ctx, gotool.CommandDir(dir), env.Values())
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
func checkToolchainProvenance(ctx context.Context, dir string, env gotool.Environment) (string, error) {
	// The composite is gofresh's: the memoized sample, then the skew
	// judgment, both refusals one typed class whose message already
	// names what this side could read beside a failed sample (spec §7's
	// refusal contract); the invocation owns the sampler memo, so a
	// composite per check does not repeat its toolchain query.
	var sample gofresh.SampleFunc
	if goVersionSampler != nil {
		sample = func(ctx context.Context, dir string, _ []string) (string, error) {
			return goVersionSampler(ctx, dir, env)
		}
	}
	provenance, err := gofresh.NewToolchainProvenance(sample)
	if err != nil {
		return "", err
	}
	checked, err := provenance.Check(ctx, dir, env.Values())
	var pe *gofresh.ToolchainProvenanceError
	if errors.As(err, &pe) {
		return "", &toolchainProvenanceError{err: pe.Err}
	}
	return checked, err
}
