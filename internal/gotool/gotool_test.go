package gotool

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// One environment policy for every go invocation (spec §9): the working
// directory resolves symlink-free and PWD pins to the resolved form, so the
// resolved-directory premise holds by construction even when the caller's
// shell exports a symlink alias as PWD.
func TestCommandDirResolvesSymlinkAlias(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink fixture")
	}
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(real, alias); err != nil {
		t.Fatal(err)
	}
	resolvedReal, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}
	if got := CommandDir(alias); got != resolvedReal {
		t.Fatalf("CommandDir(alias) = %q, want resolved %q", got, resolvedReal)
	}
	// `..` applies to the resolved prefix, never to the spelling: through
	// deep -> real/sub, deep/.. is real (the target's parent), where a
	// lexical clean before resolution answers base (the link's parent).
	// The spelling is concatenated, not Joined — filepath.Join cleans.
	sub := filepath.Join(real, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	deep := filepath.Join(base, "deep")
	if err := os.Symlink(sub, deep); err != nil {
		t.Fatal(err)
	}
	if got := CommandDir(deep + string(os.PathSeparator) + ".."); got != resolvedReal {
		t.Fatalf("CommandDir(deep/..) = %q, want the target's parent %q", got, resolvedReal)
	}
	// Resolution failure degrades to the absolute unresolved path.
	missing := filepath.Join(base, "missing")
	if got := CommandDir(missing); got != missing {
		t.Fatalf("CommandDir(missing) = %q, want the absolute path handed on", got)
	}
}

func TestCommandEnvironmentPinsPWD(t *testing.T) {
	env := []string{"HOME=/h", "PWD=/somewhere/aliased", "GOFLAGS=-count=1"}
	got, err := CommandEnvironment(env, "/resolved/dir")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(got, "PWD=/resolved/dir") {
		t.Fatalf("pinned PWD missing: %v", got)
	}
	for _, entry := range got {
		if strings.HasPrefix(entry, "PWD=") && entry != "PWD=/resolved/dir" {
			t.Fatalf("stale PWD survived: %v", got)
		}
	}
	if !slices.Contains(got, "HOME=/h") || !slices.Contains(got, "GOFLAGS=-count=1") {
		t.Fatalf("unrelated entries dropped: %v", got)
	}
	// gofresh's normalization refuses a duplicated key where the old
	// pinning replaced it silently, as pew's own class carrying
	// gofresh's message once; a nil env inherits the process's.
	_, err = CommandEnvironment([]string{"A=1", "A=2"}, "/resolved/dir")
	var envErr *EnvironmentError
	if !errors.As(err, &envErr) {
		t.Fatalf("a duplicated key was reported as %v; want the environment's class", err)
	}
	if msg := err.Error(); strings.Count(msg, "environment") != 1 {
		t.Fatalf("the class message repeats gofresh's word: %q", msg)
	}
	if inherited, err := CommandEnvironment(nil, "/resolved/dir"); err != nil || !slices.Contains(inherited, "PWD=/resolved/dir") || len(inherited) < 2 {
		t.Fatalf("a nil env did not inherit the process environment: %v, %v", inherited, err)
	}
}

func TestRunOK(t *testing.T) {
	out, err := Run(context.Background(), "env", "GOMODCACHE")
	if err != nil {
		t.Fatal(err)
	}
	if len(strings.TrimSpace(string(out))) == 0 {
		t.Error("empty GOMODCACHE")
	}
}

func TestRunError(t *testing.T) {
	if _, err := Run(context.Background(), "this-is-not-a-go-subcommand"); err == nil {
		t.Fatal("expected error")
	} else if !strings.Contains(err.Error(), "go this-is-not-a-go-subcommand") {
		t.Errorf("error not wrapped with command: %v", err)
	}
}

// run is every gofresh-runner invocation pew itself issues (Output): the
// directory resolved symlink-free and PWD pinned to it by the runner,
// the caller's entries kept, a nil env inheriting the process's, a
// refused environment its own class before any spawn, a cancelled
// context refusing the invocation — observed through the runner's
// boundary hook, the seam a pin reads the spawn through (spec §9).
func TestRunAppliesTheEnvironmentPolicy(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	// The expectation is the filesystem's own answer, never CommandDir's.
	resolved, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}
	env := []string{"PEW_RUN_MARKER=1", "PWD=/elsewhere"}
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "PWD=") && !strings.HasPrefix(kv, "PEW_RUN_MARKER=") && !strings.HasPrefix(kv, "PEW_RUN_INHERITED=") {
			env = append(env, kv)
		}
	}
	var seen *exec.Cmd
	observe := func(cmd *exec.Cmd) { seen = cmd }
	pwdOf := func(cmd *exec.Cmd) string {
		var pwd string
		for _, kv := range cmd.Env {
			if v, ok := strings.CutPrefix(kv, "PWD="); ok {
				pwd = v
			}
		}
		return pwd
	}
	out, err := run(context.Background(), link, env, observe, "env", "GOVERSION")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(out)) == "" {
		t.Fatal("empty GOVERSION")
	}
	if seen == nil {
		t.Fatal("the runner's hook never observed the spawn")
	}
	if seen.Dir != resolved {
		t.Fatalf("cmd.Dir = %q, want the resolved %q", seen.Dir, resolved)
	}
	if got := pwdOf(seen); got != resolved {
		t.Fatalf("PWD = %q, want %q", got, resolved)
	}
	if !slices.Contains(seen.Env, "PEW_RUN_MARKER=1") {
		t.Fatalf("the caller's entries were not kept: %v", seen.Env)
	}
	if seen.Args[0] != "go" || seen.Args[len(seen.Args)-1] != "GOVERSION" {
		t.Fatalf("cmd.Args = %v", seen.Args)
	}
	// A refused environment is refused as its own class before any
	// spawn — the hook never sees a command.
	seen = nil
	_, err = run(context.Background(), link, append(slices.Clone(env), "PEW_RUN_MARKER=2"), observe, "env", "GOVERSION")
	var envErr *EnvironmentError
	if !errors.As(err, &envErr) {
		t.Fatalf("a duplicated key was reported as %v; want the environment's class", err)
	}
	if seen != nil {
		t.Fatal("a refused environment reached the spawn")
	}
	// A nil env inherits the process environment (then PWD is pinned):
	// a nil-env go invocation must not run with PWD alone, or a module
	// with a toolchain directive fails to resolve without GOMODCACHE.
	t.Setenv("PEW_RUN_INHERITED", "yes")
	seen = nil
	if _, err := run(context.Background(), real, nil, observe, "env", "GOVERSION"); err != nil {
		t.Fatal(err)
	}
	if seen == nil || !slices.Contains(seen.Env, "PEW_RUN_INHERITED=yes") {
		t.Fatalf("a nil env did not inherit the process environment: %v", seen)
	}
	if got := pwdOf(seen); got != resolved {
		t.Fatalf("nil env pinned PWD = %q, want %q", got, resolved)
	}
	// The runner binds ctx: a cancelled context refuses the invocation
	// rather than running an unkillable go process.
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := run(cancelled, real, nil, nil, "env", "GOVERSION"); err == nil {
		t.Fatal("a cancelled context did not stop the go invocation")
	}
}

// A pass reader built here applies the same policy as run: its spawn
// runs in the directory resolved symlink-free with PWD pinned to it,
// the caller's entries kept, a nil env inheriting the process's, and a
// refused environment its own class before any spawn — observed
// through the boundary hook the reader takes (spec §9). The reader is
// the one route every go-env value pew reads takes.
func TestReaderAppliesTheEnvironmentPolicy(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	resolved, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}
	env := []string{"PEW_READER_MARKER=1", "PWD=/elsewhere"}
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "PWD=") && !strings.HasPrefix(kv, "PEW_READER_MARKER=") {
			env = append(env, kv)
		}
	}
	var seen *exec.Cmd
	reader, err := Reader(link, env, func(cmd *exec.Cmd) { seen = cmd })
	if err != nil {
		t.Fatal(err)
	}
	gomod, err := EnvValue(context.Background(), reader, "GOMOD")
	if err != nil {
		t.Fatal(err)
	}
	if gomod != "" && gomod != os.DevNull {
		t.Fatalf("GOMOD outside a module = %q", gomod)
	}
	if seen == nil || seen.Dir != resolved {
		t.Fatalf("the reader's spawn Dir = %v, want the resolved dir %q", seen, resolved)
	}
	if !slices.Contains(seen.Env, "PWD="+resolved) || !slices.Contains(seen.Env, "PEW_READER_MARKER=1") {
		t.Fatalf("the reader's spawn env: %v; want PWD pinned to %q and the caller's entries kept", seen.Env, resolved)
	}
	// One reader, one spawn: a second value reads the same document.
	if _, err := EnvValue(context.Background(), reader, "GOFLAGS"); err != nil {
		t.Fatal(err)
	}
	spawns := 0
	reader2, err := Reader(link, env, func(*exec.Cmd) { spawns++ })
	if err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if _, err := EnvValue(context.Background(), reader2, "GOFLAGS"); err != nil {
			t.Fatal(err)
		}
	}
	if spawns != 1 {
		t.Fatalf("three values through one reader spawned %d times, want one", spawns)
	}
	t.Setenv("PEW_READER_INHERITED", "yes")
	seen = nil
	reader3, err := Reader(link, nil, func(cmd *exec.Cmd) { seen = cmd })
	if err != nil {
		t.Fatal(err)
	}
	if _, err := EnvValue(context.Background(), reader3, "GOMOD"); err != nil || seen == nil || !slices.Contains(seen.Env, "PEW_READER_INHERITED=yes") {
		t.Fatalf("a nil env did not inherit the process environment: %v, %v", err, seen)
	}
	var envErr *EnvironmentError
	if _, err := Reader(link, append(slices.Clone(env), "PEW_READER_MARKER=2"), nil); !errors.As(err, &envErr) {
		t.Fatalf("a duplicated key built a reader: %v; want pew's environment class before any spawn", err)
	}
}
