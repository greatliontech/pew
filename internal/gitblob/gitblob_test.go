package gitblob

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

// Open against the pew repository itself, then read a committed file at HEAD.
// This exercises the real go-git read path (ref resolution → commit → blob)
// without constructing a throwaway repo.
func TestReadAtHEAD(t *testing.T) {
	wd, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	repo, err := Open(wd)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// go.mod is committed at the repo root and stable across history.
	abs := filepath.Join(repo.root, "go.mod")
	content, ok, err := repo.ReadAt("HEAD", abs)
	if err != nil {
		t.Fatalf("ReadAt go.mod@HEAD: %v", err)
	}
	if !ok {
		t.Fatal("go.mod not found at HEAD")
	}
	if !strings.Contains(string(content), "module github.com/thegrumpylion/pew") {
		t.Errorf("go.mod@HEAD missing module line:\n%s", content)
	}
}

// A path that exists in the working tree but was never committed reads as
// not-found (ok=false), not an error — the "added since baseline" case.
func TestReadAtMissingFile(t *testing.T) {
	repo := openRepo(t)
	abs := filepath.Join(repo.root, "this-file-does-not-exist-in-git.txt")
	_, ok, err := repo.ReadAt("HEAD", abs)
	if err != nil {
		t.Fatalf("ReadAt missing: unexpected error %v", err)
	}
	if ok {
		t.Error("ok = true for a path absent at HEAD; want false")
	}
}

func TestReadAtBadRef(t *testing.T) {
	repo := openRepo(t)
	abs := filepath.Join(repo.root, "go.mod")
	if _, _, err := repo.ReadAt("no-such-ref-xyz", abs); err == nil {
		t.Error("ReadAt with a bogus ref returned nil error")
	}
}

func TestListAndReadAtSkipSymlinkBlobs(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "benchmarks", "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	regular := filepath.Join(dir, "benchmarks", "pkg", "BenchmarkRegular.txt")
	if err := os.WriteFile(regular, []byte("BenchmarkRegular-8 1 1 sec/op\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(dir, "benchmarks", "pkg", "BenchmarkSymlink.txt")
	if err := os.Symlink("BenchmarkRegular.txt", symlink); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	raw, err := gogit.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("git init: %v", err)
	}
	ref := commitAll(t, raw)
	repo, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	paths, err := repo.ListAt(ref.String(), filepath.Join(dir, "benchmarks"))
	if err != nil {
		t.Fatalf("ListAt: %v", err)
	}
	if !containsPath(paths, regular) {
		t.Fatalf("ListAt omitted regular blob: %v", paths)
	}
	if containsPath(paths, symlink) {
		t.Fatalf("ListAt included symlink blob: %v", paths)
	}
	if _, ok, err := repo.ReadAt(ref.String(), symlink); err != nil || ok {
		t.Fatalf("ReadAt symlink ok=%v err=%v, want ok=false nil err", ok, err)
	}
}

// A path outside the repository is rejected rather than silently treated as
// not-recorded — surfacing a misconfigured --bench-dir.
func TestRelPathOutsideRepo(t *testing.T) {
	repo := &Repo{root: filepath.FromSlash("/tmp/some/repo")}
	if _, err := repo.relPath(filepath.FromSlash("/etc/passwd")); err == nil {
		t.Error("relPath accepted a path outside the repo root")
	}
	rel, err := repo.relPath(filepath.FromSlash("/tmp/some/repo/benchmarks/pkg/BenchmarkX.txt"))
	if err != nil {
		t.Fatalf("relPath in-repo: %v", err)
	}
	if rel != "benchmarks/pkg/BenchmarkX.txt" {
		t.Errorf("relPath = %q, want benchmarks/pkg/BenchmarkX.txt", rel)
	}
}

func TestExistsAt(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "data", "fixture.txt"), []byte("y"), 0o644); err != nil {
		t.Fatal(err)
	}
	raw, err := gogit.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("git init: %v", err)
	}
	ref := commitAll(t, raw)
	// Created after the commit → present on disk, absent from the tree.
	if err := os.WriteFile(filepath.Join(dir, "uncommitted.dat"), []byte("z"), 0o644); err != nil {
		t.Fatal(err)
	}
	repo, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	for _, tc := range []struct {
		name, path string
		want       bool
	}{
		{"tracked file", "tracked.txt", true},
		{"tracked directory", "data", true},
		{"tracked nested file", "data/fixture.txt", true},
		{"uncommitted file", "uncommitted.dat", false},
		{"missing path", "nope.txt", false},
		{"missing under dir", "data/nope.txt", false},
	} {
		got, err := repo.ExistsAt(ref.String(), filepath.Join(dir, filepath.FromSlash(tc.path)))
		if err != nil {
			t.Fatalf("%s: ExistsAt: %v", tc.name, err)
		}
		if got != tc.want {
			t.Errorf("%s: ExistsAt = %v, want %v", tc.name, got, tc.want)
		}
	}
	if _, err := repo.ExistsAt("no-such-ref", filepath.Join(dir, "tracked.txt")); err == nil {
		t.Error("ExistsAt with a bad ref: want error, got nil")
	}
}

func openRepo(t *testing.T) *Repo {
	t.Helper()
	wd, err := filepath.Abs(".")
	if err != nil {
		t.Fatal(err)
	}
	repo, err := Open(wd)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return repo
}

func containsPath(paths []string, want string) bool {
	for _, path := range paths {
		if path == want {
			return true
		}
	}
	return false
}

func commitAll(t *testing.T, repo *gogit.Repository) plumbing.Hash {
	t.Helper()
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("worktree: %v", err)
	}
	if err := wt.AddGlob("."); err != nil {
		t.Fatalf("git add: %v", err)
	}
	h, err := wt.Commit("commit", &gogit.CommitOptions{Author: &object.Signature{Name: "pew test", Email: "pew@example.invalid", When: time.Unix(1, 0)}})
	if err != nil {
		t.Fatalf("git commit: %v", err)
	}
	return h
}
