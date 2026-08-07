// Package gotool runs the go command line tool, surfacing stderr on failure.
// Every invocation runs under one environment policy: the working directory
// resolved symlink-free and PWD pinned to it, so the resolved-directory
// premise every consumer of go-reported paths relies on holds by
// construction — even through a symlinked checkout whose shell exports the
// alias as PWD (spec §9's environment policy).
package gotool

import (
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

// Run executes `go <args>` in the current directory. See RunIn.
func Run(args ...string) ([]byte, error) { return RunIn("", args...) }

// RunIn executes `go <args>` in dir ("" = current directory) and returns stdout.
// On failure the error includes the command and go's stderr. The directory
// matters: a go.mod `toolchain` directive / GOTOOLCHAIN is resolved relative to
// it, so provenance capture and `go test` must run in the same dir to describe
// the same toolchain. The directory is resolved and PWD pinned per the
// package policy above.
func RunIn(dir string, args ...string) ([]byte, error) {
	resolved := CommandDir(dir)
	cmd := exec.Command("go", args...)
	cmd.Dir = resolved
	cmd.Env = CommandEnvironment(os.Environ(), resolved)
	out, err := cmd.Output()
	if err != nil {
		if ee, ok := errors.AsType[*exec.ExitError](err); ok {
			return nil, fmt.Errorf("go %s: %w: %s",
				strings.Join(args, " "), err, strings.TrimSpace(string(ee.Stderr)))
		}
		return nil, fmt.Errorf("go %s: %w", strings.Join(args, " "), err)
	}
	return out, nil
}
