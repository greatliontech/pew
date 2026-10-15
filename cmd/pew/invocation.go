package main

import (
	"context"
	"io"
	"os/exec"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/pew/internal/gotool"
)

// invocation owns policy and preparation for one judged command. Its context
// binding carries ownership through cancellation and bounded publication contexts.
type invocation struct {
	env         gotool.Environment
	vouches     *vouchSource
	diagnostics func(gofresh.Progress)
	errw        io.Writer
}

type invocationKey struct{}

type executionDependencies struct {
	sample  func(context.Context, string, gotool.Environment) (string, error)
	prepare func(*exec.Cmd)
	view    func(*analysisSession, context.Context, []gofresh.Subject, string, gofresh.Kind) (*gofresh.RuntimeBinding, error)
}
type dependenciesKey struct{}

func dependencies(ctx context.Context) *executionDependencies {
	if d, ok := ctx.Value(dependenciesKey{}).(*executionDependencies); ok {
		return d
	}
	return &executionDependencies{sample: sampleGoVersion, view: func(e *analysisSession, ctx context.Context, subjects []gofresh.Subject, dir string, kind gofresh.Kind) (*gofresh.RuntimeBinding, error) {
		return e.bind(ctx, subjects, kind)
	}}
}

func beginInvocation(ctx context.Context, storeDir string, entries []string, errw io.Writer) (context.Context, *invocation, error) {
	vouches, err := newVouchSource(storeDir, entries)
	if err != nil {
		return ctx, nil, err
	}
	if storeDir != "" {
		if _, err := vouches.engineVouches(""); err != nil {
			return ctx, nil, err
		}
	}
	env, err := gotool.NewEnvironment(nil)
	if err != nil {
		return ctx, nil, err
	}
	i := &invocation{env: env, vouches: vouches, diagnostics: gofresh.DiagnosticsTo(errw), errw: errw}
	ctx = withToolchainSampler(ctx)
	return context.WithValue(ctx, invocationKey{}, i), i, nil
}

func invocationVouches(ctx context.Context, moduleDir string) ([]string, error) {
	if i, ok := ctx.Value(invocationKey{}).(*invocation); ok {
		return i.vouches.engineVouches(moduleDir)
	}
	// Direct engine callers own a single independent preparation operation.
	s, err := newVouchSource("", nil)
	if err != nil {
		return nil, err
	}
	return s.engineVouches(moduleDir)
}
