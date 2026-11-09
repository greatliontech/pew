package main

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"

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

var errNoSampler = errors.New("toolchain provenance: no sampler — a consumer holds one memoized sampler per judged run")

// withToolchainSampler owns the preparation readers for one judged invocation.
// Toolchain provenance and build configuration read the same snapshot. Write
// validation constructs a fresh reader instead of serving preparation evidence.
func withToolchainSampler(ctx context.Context) context.Context {
	var next invocation
	if prev := currentInvocation(ctx); prev != nil {
		next = *prev
	}
	next.readers = &gotool.RunMemo[*gotool.EnvReader]{}
	return context.WithValue(ctx, invocationKey{}, &next)
}

func preparationReader(ctx context.Context, dir string, env gotool.Environment) (*gotool.EnvReader, error) {
	dir = resolveCommandDir(ctx, dir)
	makeReader := func() *gotool.EnvReader {
		return gotool.Reader(dir, env, func(cmd *exec.Cmd) {
			if hook := dependencies(ctx).prepare; hook != nil {
				hook(cmd)
			}
		})
	}
	var memo *gotool.RunMemo[*gotool.EnvReader]
	if inv := currentInvocation(ctx); inv != nil {
		memo = inv.readers
	}
	if memo == nil {
		return makeReader(), nil
	}
	return memo.Get(gotool.CommandDir(dir), env.Values(), makeReader)
}

// sampleGoVersion reads the toolchain sample through pew's one go-command
// policy and gotool's snapshot sampler — the sample resolves as the engine's own
// loads do, the module's go.mod toolchain directive included, under
// the effective environment.
func sampleGoVersion(ctx context.Context, dir string, env gotool.Environment) (string, error) {
	reader, err := preparationReader(ctx, dir, env)
	if err != nil {
		return "", err
	}
	return (gotool.SnapshotSampler{Reader: reader}).Sample(ctx, resolveCommandDir(ctx, dir), env.Values())
}

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
	if dir == "" || !filepath.IsAbs(dir) {
		dir = resolveCommandDir(ctx, dir)
	}
	goVersionSampler := dependencies(ctx).sample
	if goVersionSampler == nil {
		return "", errNoSampler
	}
	ambient, err := goVersionSampler(ctx, dir, env)
	if err != nil {
		if ctx != nil && ctx.Err() != nil && errors.Is(err, ctx.Err()) {
			return "", err
		}
		var envErr *gotool.EnvironmentError
		if errors.As(err, &envErr) {
			return "", err
		}
		return "", &toolchainProvenanceError{err: fmt.Errorf("toolchain provenance: binary built with %s, ambient toolchain unidentifiable — refusing to judge: %w", runtime.Version(), err)}
	}
	if err := gofresh.ToolchainSkew(ambient); err != nil {
		return "", &toolchainProvenanceError{err: err}
	}
	return ambient, nil
}
