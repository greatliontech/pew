package gotool

import (
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
	out, err := Run("env", "GOMODCACHE")
	if err != nil {
		t.Fatal(err)
	}
	if len(strings.TrimSpace(string(out))) == 0 {
		t.Error("empty GOMODCACHE")
	}
}

func TestRunError(t *testing.T) {
	if _, err := Run("this-is-not-a-go-subcommand"); err == nil {
		t.Fatal("expected error")
	} else if !strings.Contains(err.Error(), "go this-is-not-a-go-subcommand") {
		t.Errorf("error not wrapped with command: %v", err)
	}
}
