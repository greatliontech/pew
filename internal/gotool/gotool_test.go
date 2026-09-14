package gotool

import (
	"context"
	"os"
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
	// Resolution failure degrades to the absolute unresolved path.
	missing := filepath.Join(base, "missing")
	if got := CommandDir(missing); got != missing {
		t.Fatalf("CommandDir(missing) = %q, want the absolute path handed on", got)
	}
}

func TestCommandEnvironmentPinsPWD(t *testing.T) {
	env := []string{"HOME=/h", "PWD=/somewhere/aliased", "GOFLAGS=-count=1"}
	got := CommandEnvironment(env, "/resolved/dir")
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

// Command is the one go-command constructor: it resolves the directory
// symlink-free and pins PWD to it, so every go invocation pew makes
// shares the environment policy (spec §9).
func TestCommandAppliesTheEnvironmentPolicy(t *testing.T) {
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	resolved := CommandDir(link)
	cmd := Command(context.Background(), link, []string{"A=1", "PWD=/elsewhere"}, "env", "GOVERSION")
	if cmd.Dir != resolved {
		t.Fatalf("cmd.Dir = %q, want %q", cmd.Dir, resolved)
	}
	var pwd string
	for _, kv := range cmd.Env {
		if v, ok := strings.CutPrefix(kv, "PWD="); ok {
			pwd = v
		}
	}
	if pwd != resolved {
		t.Fatalf("PWD = %q, want %q", pwd, resolved)
	}
	if cmd.Args[0] != "go" || cmd.Args[len(cmd.Args)-1] != "GOVERSION" {
		t.Fatalf("cmd.Args = %v", cmd.Args)
	}
	// The constructor binds ctx: a cancelled context refuses the
	// invocation rather than running an unkillable go process.
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := Command(cancelled, real, nil, "env", "GOVERSION").Run(); err == nil {
		t.Fatal("a cancelled context did not stop the go invocation")
	}
	// A nil env inherits the process environment (then PWD is pinned):
	// a nil-env go invocation must not run with PWD alone, or a module
	// with a toolchain directive fails to resolve without GOMODCACHE.
	nilEnv := Command(context.Background(), real, nil, "env", "GOVERSION").Env
	if len(nilEnv) <= 1 {
		t.Fatalf("nil env ran go with %d vars; want the inherited environment plus PWD", len(nilEnv))
	}
	if !slices.ContainsFunc(nilEnv, func(kv string) bool { return strings.HasPrefix(kv, "PWD=") }) {
		t.Fatalf("nil env did not pin PWD: %v", nilEnv)
	}
}
