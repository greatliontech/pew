package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	runpkg "github.com/thegrumpylion/pew/internal/run"
	"github.com/thegrumpylion/pew/internal/runtimeinputs"
)

// TestRuntimeInputsUncommitted pins the dirty-marking (§5, §7.8, Q3-A): a runtime
// input under the module but absent at the run commit (e.g. a .gitignore'd fixture,
// created after commit) makes the recording not reproducible from that commit, so
// it is marked dirty; a committed input does not.
func TestRuntimeInputsUncommitted(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "committed.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	raw, err := gogit.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("git init: %v", err)
	}
	wt, err := raw.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if err := wt.AddGlob("."); err != nil {
		t.Fatal(err)
	}
	commit, err := wt.Commit("c", &gogit.CommitOptions{Author: &object.Signature{Name: "t", Email: "t@example.invalid", When: time.Unix(1, 0)}})
	if err != nil {
		t.Fatal(err)
	}
	// An input created after the commit (stands in for a .gitignore'd/untracked fixture).
	if err := os.WriteFile(filepath.Join(dir, "secret.dat"), []byte("s"), 0o644); err != nil {
		t.Fatal(err)
	}

	manifestFor := func(rel string) string {
		t.Helper()
		st, err := runtimeinputs.FromTestLog([]byte("# test log\nopen "+rel+"\n"), dir, dir)
		if err != nil {
			t.Fatalf("FromTestLog(%s): %v", rel, err)
		}
		return st.Manifest
	}

	if u, err := runtimeInputsUncommitted(dir, commit.String(), manifestFor("committed.txt")); err != nil || u {
		t.Errorf("committed input: uncommitted=%v err=%v, want false", u, err)
	}
	if u, err := runtimeInputsUncommitted(dir, commit.String(), manifestFor("secret.dat")); err != nil || !u {
		t.Errorf("uncommitted input: uncommitted=%v err=%v, want true", u, err)
	}
}

// TestBenchmarkCandidatePaths pins the Prime batch filter: only in-module packages
// with test files are candidates (they are the only packages status/run can
// Compute, since selectedBenchmarks reads TestGoFiles/XTestGoFiles). Priming a
// non-test or out-of-module package would build whole-program SSA the loop never
// uses — the waste the filter exists to avoid.
func TestBenchmarkCandidatePaths(t *testing.T) {
	mk := func(path, moduleDir string, testFiles, xtestFiles []string) pkgMeta {
		p := pkgMeta{ImportPath: path, TestGoFiles: testFiles, XTestGoFiles: xtestFiles}
		p.Module.Dir = moduleDir
		return p
	}
	pkgs := []pkgMeta{
		mk("ex/withtest", "/m", []string{"a_test.go"}, nil),      // in-module, in-package test → candidate
		mk("ex/withxtest", "/m", nil, []string{"x_test.go"}),     // in-module, external test → candidate
		mk("ex/notest", "/m", nil, nil),                          // in-module, no test files → excluded
		mk("ex/nomodule", "", []string{"a_test.go"}, nil),        // not in a module → excluded
	}
	got := benchmarkCandidatePaths(pkgs)
	want := []string{"ex/withtest", "ex/withxtest"}
	if len(got) != len(want) {
		t.Fatalf("benchmarkCandidatePaths = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("candidate[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestRunRunReturnsErrorOnPackageFailure(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/runfail\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "p_test.go"), []byte("package runfail\n\nimport \"testing\"\n\nfunc BenchmarkX(b *testing.B) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	var out, errOut bytes.Buffer
	err := runRun(&out, &errOut, runConfig{opts: runpkg.Options{Count: 1, Benchtime: "1x", Bench: "."}}, []string{"./..."})
	if err == nil {
		t.Fatal("runRun succeeded despite a per-package failure")
	}
	if !strings.Contains(out.String(), "error") {
		t.Fatalf("output = %q, want package error row", out.String())
	}
}
