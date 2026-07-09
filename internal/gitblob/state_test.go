package gitblob

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestStateDirty(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init")
	git("config", "user.email", "t@example.com")
	git("config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("commit", "-m", "init")

	commit, dirty, err := State(dir)
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if len(commit) != 40 {
		t.Errorf("commit sha: got %q (len %d), want 40 hex", commit, len(commit))
	}
	if dirty {
		t.Error("freshly-committed repo reported dirty")
	}

	if err := os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, dirty, err = State(dir); err != nil {
		t.Fatalf("State after change: %v", err)
	} else if !dirty {
		t.Error("repo with an untracked file reported clean")
	}
}
