package gitblob

import (
	"path/filepath"
	"strings"
	"testing"
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
