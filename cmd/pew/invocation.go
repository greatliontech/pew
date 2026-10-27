package main

import (
	"context"
	"io"
	"os/exec"
	"path/filepath"

	"github.com/greatliontech/gofresh"
	gofreshtool "github.com/greatliontech/gofresh/gotool"
	"github.com/greatliontech/pew/internal/gotool"
)

// invocation owns policy and preparation for one judged command. Its context
// binding carries ownership through cancellation and bounded publication contexts.
type invocation struct {
	workDir     string
	env         gotool.Environment
	vouches     *vouchSource
	diagnostics func(gofresh.Progress)
	errw        io.Writer
	sessions    map[sessionKey]*sessionEntry
	readers     *gofreshtool.RunMemo[*gofreshtool.EnvReader]
	deps        *executionDependencies
}

type sessionEntry struct {
	session  *gofresh.Session
	seenDirs map[string]bool
}

type sessionKey struct {
	moduleDir string
	env       string
	pgo       string
	vouches   string
}

type invocationKey struct{}

type executionDependencies struct {
	sample  func(context.Context, string, gotool.Environment) (string, error)
	prepare func(*exec.Cmd)
	view    func(*analysisSession, context.Context, []gofresh.Subject, string, gofresh.Kind) (*gofresh.RuntimeBinding, error)
}

func currentInvocation(ctx context.Context) *invocation {
	inv, _ := ctx.Value(invocationKey{}).(*invocation)
	return inv
}

func withDependencies(ctx context.Context, d *executionDependencies) context.Context {
	var next invocation
	if prev := currentInvocation(ctx); prev != nil {
		next = *prev
	}
	next.deps = d
	return context.WithValue(ctx, invocationKey{}, &next)
}

func withWorkDir(ctx context.Context, workDir string) context.Context {
	if workDir != "" && !filepath.IsAbs(workDir) {
		if abs, err := filepath.Abs(workDir); err == nil {
			workDir = abs
		}
	}
	var next invocation
	if prev := currentInvocation(ctx); prev != nil {
		next = *prev
	}
	next.workDir = workDir
	return context.WithValue(ctx, invocationKey{}, &next)
}

func invocationWorkDir(ctx context.Context) string {
	if inv := currentInvocation(ctx); inv != nil && inv.workDir != "" {
		return inv.workDir
	}
	return "."
}

func resolveWorkPath(ctx context.Context, path string) (string, error) {
	if path == "" {
		path = invocationWorkDir(ctx)
	} else if !filepath.IsAbs(path) {
		path = filepath.Join(invocationWorkDir(ctx), path)
	}
	return filepath.Abs(path)
}

func resolveCommandDir(ctx context.Context, dir string) string {
	if dir == "" {
		dir = invocationWorkDir(ctx)
	} else if !filepath.IsAbs(dir) {
		dir = filepath.Join(invocationWorkDir(ctx), dir)
	}
	return gotool.CommandDir(dir)
}

func dependencies(ctx context.Context) *executionDependencies {
	if inv := currentInvocation(ctx); inv != nil && inv.deps != nil {
		return inv.deps
	}
	return &executionDependencies{sample: sampleGoVersion, view: func(e *analysisSession, ctx context.Context, subjects []gofresh.Subject, dir string, kind gofresh.Kind) (*gofresh.RuntimeBinding, error) {
		return e.bind(ctx, subjects, kind)
	}}
}

func beginInvocation(ctx context.Context, storeDir string, entries []string, errw io.Writer) (context.Context, *invocation, error) {
	var deps *executionDependencies
	var workDir string
	if prev := currentInvocation(ctx); prev != nil {
		deps = prev.deps
		workDir = prev.workDir
	}
	if workDir == "" {
		if abs, err := filepath.Abs("."); err == nil {
			workDir = abs
		} else {
			workDir = "."
		}
	}
	if storeDir != "" && !filepath.IsAbs(storeDir) {
		storeDir = filepath.Join(workDir, storeDir)
	}
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
	i := &invocation{
		workDir:     workDir,
		env:         env,
		vouches:     vouches,
		diagnostics: gofresh.DiagnosticsTo(errw),
		errw:        errw,
		readers:     &gofreshtool.RunMemo[*gofreshtool.EnvReader]{},
		deps:        deps,
	}
	return context.WithValue(ctx, invocationKey{}, i), i, nil
}

func invocationVouches(ctx context.Context, moduleDir string) ([]string, error) {
	if i := currentInvocation(ctx); i != nil && i.vouches != nil {
		return i.vouches.engineVouches(moduleDir)
	}
	// Direct engine callers own a single independent preparation operation.
	s, err := newVouchSource("", nil)
	if err != nil {
		return nil, err
	}
	return s.engineVouches(moduleDir)
}
