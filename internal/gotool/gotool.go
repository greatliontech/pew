// Package gotool runs the go command line tool, surfacing stderr on failure.
// Every invocation runs under gofresh's one go-command policy (gofresh's
// gotool): the working directory resolved to its one coordinate and PWD
// pinned to it, so the resolved-directory premise every consumer of
// go-reported paths relies on holds by construction — even through a
// symlinked checkout whose shell exports the alias as PWD (spec §9's
// environment policy). This package composes pew's two contracts around
// gofresh's: a directory that does not resolve degrades to its absolute
// spelling, and a nil environment inherits the process's. Every go
// invocation pew makes takes one of three routes, each under the same
// composition: run here through gofresh's runner (Output), spawned
// through a pass reader built here (Reader — gofresh's own analysis
// entries and pew's go-env reads alike), or — where the spawn needs its
// own process group (internal/run's runCommand) — built over CommandDir
// and CommandEnvironment. Resolve is the one composition the first two
// share: the directory to its coordinate, the environment inherited
// and refused as pew's class.
package gotool

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"

	gofreshtool "github.com/greatliontech/gofresh/gotool"
)

// CommandDir resolves dir ("" = current directory) to gofresh's one
// coordinate (gotool.CanonicalDir: the absolute spelling with every
// symbolic link followed, `..` applied to the resolved prefix — through
// `deep -> real/sub`, `deep/..` is real, the target's parent, where a
// lexical clean of the spelling first would answer the link's parent).
// Resolution failure degrades to the absolute unresolved path, else the
// spelling given, so the subsequent invocation fails (or succeeds) on
// the real filesystem state rather than here.
func CommandDir(dir string) string {
	if dir == "" {
		dir = "."
	}
	if canonical, err := gofreshtool.CanonicalDir(dir); err == nil {
		return canonical
	}
	if abs, err := filepath.Abs(dir); err == nil {
		return abs
	}
	return dir
}

// EnvironmentError is a caller environment gofresh's normalization
// refuses — a duplicated or malformed entry — its own class, never a
// fact about the toolchain or the tree. The message is gofresh's own,
// which already names the environment; the class is the type.
type EnvironmentError struct{ Err error }

func (e *EnvironmentError) Error() string { return e.Err.Error() }
func (e *EnvironmentError) Unwrap() error { return e.Err }

// inherited is env, or the process environment when env is nil: a nil
// env inherits the process environment, as an unset exec.Cmd.Env
// would — then PWD is pinned over it. A caller passing the empty
// non-nil slice deliberately runs go with PWD alone.
func inherited(env []string) []string {
	if env == nil {
		return os.Environ()
	}
	return env
}

// CommandEnvironment is gofresh's command environment over env for
// resolvedDir: PWD pinned to the directory under the platform's key
// rule (gotool.EnvForCommand). A nil env inherits the process
// environment; an environment gofresh's normalization refuses is an
// EnvironmentError.
func CommandEnvironment(env []string, resolvedDir string) ([]string, error) {
	out, err := gofreshtool.EnvForCommand(inherited(env), resolvedDir)
	if err != nil {
		return nil, &EnvironmentError{Err: err}
	}
	return out, nil
}

// Output runs `go <args>` in dir under env through gofresh's runner and
// returns stdout; on failure the error names the command and carries
// go's stderr (the runner's own wrapping). An environment the policy
// refuses is an EnvironmentError before any spawn — it names no command.
func Output(ctx context.Context, dir string, env []string, args ...string) ([]byte, error) {
	return run(ctx, dir, env, nil, args...)
}

// EnvValue is one go-env value read through a pass reader: the
// reader's one `go env -json` snapshot (taken on its first ask, served
// to every later one — gofresh's published form), the key read from
// the pass's document. A key the go command does not set reads empty;
// a nil reader is refused as gofresh's own analysis entries refuse it.
// Every go-env value pew wants from one pass comes from that pass's
// reader, never a per-key probe.
func EnvValue(ctx context.Context, reader *gofreshtool.EnvReader, key string) (string, error) {
	if reader == nil {
		return "", errors.New("gotool: nil pass reader")
	}
	snap, err := reader.Snapshot(ctx)
	if err != nil {
		return "", err
	}
	return snap.Value(key), nil
}

// Runner is gofresh's runner under pew's boundary hook: the one spawn
// policy every go child pew makes through gofresh rides — Command's
// derived environment over a resolved directory. prepare, when set, is
// the hook a pin observes the command's directory and environment
// through.
func Runner(prepare func(*exec.Cmd)) gofreshtool.Runner {
	return gofreshtool.Runner{Prepare: prepare}
}

// Resolve resolves the caller's directory and environment under pew's
// policy ahead of a gofresh spawn: the directory to its coordinate, a
// nil environment inherited, an environment the policy refuses named as
// pew's own class before any command exists. The normalization runs
// here ahead of the runner's own (the same predicate, inside
// gotool.EnvForCommand) deliberately: gofresh reports a refusal as an
// untyped message under the command's name, so the pre-check is what
// lets a caller see pew's class — and once it passes, the runner's
// second pass cannot newly fail, so an environment refusal is always
// pew's class.
func Resolve(dir string, env []string) (string, []string, error) {
	env = inherited(env)
	if _, err := gofreshtool.NormalizeEnv(env); err != nil {
		return "", nil, &EnvironmentError{Err: err}
	}
	return CommandDir(dir), env, nil
}

// Reader is a pass's go-env reader (gotool.EnvReader: the first key
// takes the pass's one `go env -json` snapshot) over the directory and
// environment Resolve answers, its spawns under Runner(prepare) — the
// value gofresh's analysis entries take, built under pew's policy.
func Reader(dir string, env []string, prepare func(*exec.Cmd)) (*gofreshtool.EnvReader, error) {
	resolved, env, err := Resolve(dir, env)
	if err != nil {
		return nil, err
	}
	return gofreshtool.NewEnvReader(Runner(prepare), resolved, env), nil
}

// run is every gofresh-runner invocation pew itself issues — gofresh's
// analysis entries spawn through Reader's: the directory and
// environment through Resolve, the hook handed to the runner. The
// runner's other refusal, a directory with no absolute form (an
// unreadable working directory, CommandDir degraded to the spelling
// given), is gofresh's own message and not an environment fact.
func run(ctx context.Context, dir string, env []string, prepare func(*exec.Cmd), args ...string) ([]byte, error) {
	resolved, env, err := Resolve(dir, env)
	if err != nil {
		return nil, err
	}
	return Runner(prepare).Run(ctx, resolved, env, args...)
}

// Run executes `go <args>` in the current directory under ctx. See RunIn.
func Run(ctx context.Context, args ...string) ([]byte, error) { return RunIn(ctx, "", args...) }

// RunIn executes `go <args>` in dir ("" = current directory) under ctx and
// returns stdout, over the ambient process environment. The directory
// matters: a go.mod `toolchain` directive / GOTOOLCHAIN is resolved
// relative to it, so provenance capture and `go test` must run in the
// same dir to describe the same toolchain — the directory is resolved
// and PWD pinned per Output.
func RunIn(ctx context.Context, dir string, args ...string) ([]byte, error) {
	return Output(ctx, dir, nil, args...)
}
