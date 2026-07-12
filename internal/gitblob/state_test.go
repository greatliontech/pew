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
	clean, err := Snapshot(dir)
	if err != nil {
		t.Fatalf("Snapshot clean: %v", err)
	}
	benchDir := filepath.Join(dir, "benchmarks")
	if err := os.Mkdir(benchDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(benchDir, "BenchmarkX.txt"), []byte("result"), 0o644); err != nil {
		t.Fatal(err)
	}
	withRecording, err := Snapshot(dir)
	if err != nil {
		t.Fatalf("Snapshot with recording: %v", err)
	}
	if clean.Equal(withRecording) {
		t.Fatal("recording write did not change exact repository state")
	}
	recordingPath := filepath.Join(benchDir, "BenchmarkX.txt")
	if !clean.EqualExceptPaths(withRecording, []string{recordingPath}) {
		t.Fatal("recording-file change was not excluded")
	}
	if clean.EqualExceptPaths(withRecording, []string{filepath.Join(benchDir, "other.txt")}) {
		t.Fatal("unlisted recording-root change was excluded")
	}
	if err := os.RemoveAll(benchDir); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, dirty, err = State(dir); err != nil {
		t.Fatalf("State after change: %v", err)
	} else if !dirty {
		t.Error("repo with an untracked file reported clean")
	}
	firstDirty, err := Snapshot(dir)
	if err != nil {
		t.Fatalf("Snapshot dirty: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("z"), 0o644); err != nil {
		t.Fatal(err)
	}
	secondDirty, err := Snapshot(dir)
	if err != nil {
		t.Fatalf("Snapshot changed dirty: %v", err)
	}
	if firstDirty.Equal(secondDirty) {
		t.Fatal("different dirty contents produced equal repository states")
	}
}
