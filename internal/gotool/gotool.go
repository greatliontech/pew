// Package gotool runs the go command line tool, surfacing stderr on failure.
// Every invocation runs under one environment policy: the working directory
// resolved symlink-free and PWD pinned to it, so the resolved-directory
// premise every consumer of go-reported paths relies on holds by
// construction — even through a symlinked checkout whose shell exports the
// alias as PWD (spec §9's environment policy).
package gotool

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// CommandDir resolves dir ("" = current directory) to its symlink-free
// absolute form. Resolution failure degrades to the absolute unresolved
// path: the subsequent invocation then fails (or succeeds) on the real
// filesystem state rather than here.
func CommandDir(dir string) string {
	if dir == "" {
		dir = "."
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return dir
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return abs
	}
	return resolved
}

// CommandEnvironment pins PWD to resolvedDir over env, the one environment
// policy for every go invocation pew makes.
func CommandEnvironment(env []string, resolvedDir string) []string {
	command := make([]string, 0, len(env)+1)
	for _, entry := range env {
		name, _, ok := strings.Cut(entry, "=")
		if ok && equalEnvKey(name, "PWD") {
			continue
		}
		command = append(command, entry)
	}
	return append(command, "PWD="+resolvedDir)
}

func equalEnvKey(left, right string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

// Command is the one go-command constructor: `go <args>` under ctx, in
// dir ("" = current directory) with the environment policy applied —
// the directory resolved symlink-free and PWD pinned to it. Every go
// invocation pew makes is built here, so §9's resolved-directory
// premise holds for all of them by construction, the toolchain-
// provenance sample (§7) included. Cancelling ctx kills the go
// process; a caller that must also kill a grandchild (the test binary
// go test spawns) runs its own process group over the same policy.
func Command(ctx context.Context, dir string, env []string, args ...string) *exec.Cmd {
	if env == nil {
		// A nil env inherits the process environment, as an unset
		// exec.Cmd.Env would — then PWD is pinned over it. A caller
		// passing the empty non-nil slice deliberately runs go with
		// PWD alone.
		env = os.Environ()
	}
	resolved := CommandDir(dir)
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = resolved
	cmd.Env = CommandEnvironment(env, resolved)
	return cmd
}

// Run executes `go <args>` in the current directory under ctx. See RunIn.
func Run(ctx context.Context, args ...string) ([]byte, error) { return RunIn(ctx, "", args...) }

// RunIn executes `go <args>` in dir ("" = current directory) under ctx and
// returns stdout, over the ambient process environment. On failure the error
// includes the command and go's stderr. The directory matters: a go.mod
// `toolchain` directive / GOTOOLCHAIN is resolved relative to it, so
// provenance capture and `go test` must run in the same dir to describe the
// same toolchain — the directory is resolved and PWD pinned per Command.
func RunIn(ctx context.Context, dir string, args ...string) ([]byte, error) {
	out, err := Command(ctx, dir, os.Environ(), args...).Output()
	if err != nil {
		if ee, ok := errors.AsType[*exec.ExitError](err); ok {
			return nil, fmt.Errorf("go %s: %w: %s",
				strings.Join(args, " "), err, strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("go %s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}
