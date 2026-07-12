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
	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/runtimeinput"
	"github.com/greatliontech/pew/internal/gitblob"
	runpkg "github.com/greatliontech/pew/internal/run"
	"github.com/greatliontech/pew/internal/store"
	"golang.org/x/perf/benchfmt"
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

	stateFor := func(rel string) runtimeinput.State {
		t.Helper()
		st, err := runtimeinput.FromTestLog([]byte("# test log\nopen "+rel+"\n"), dir, dir)
		if err != nil {
			t.Fatalf("FromTestLog(%s): %v", rel, err)
		}
		return st
	}

	if u, err := runtimeInputsDirty(dir, commit.String(), stateFor("committed.txt")); err != nil || u {
		t.Errorf("committed input: uncommitted=%v err=%v, want false", u, err)
	}
	if u, err := runtimeInputsDirty(dir, commit.String(), stateFor("secret.dat")); err != nil || !u {
		t.Errorf("uncommitted input: uncommitted=%v err=%v, want true", u, err)
	}
}

func TestRequiredBenchmarksIncludesCurrentImpureSelection(t *testing.T) {
	all := []string{"BenchmarkA", "BenchmarkB", "BenchmarkC"}
	got := requiredBenchmarks(all, []string{"BenchmarkC"}, map[string]bool{"BenchmarkA": true})
	want := []string{"BenchmarkA", "BenchmarkC"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("required benchmarks = %v, want %v", got, want)
	}
}

func TestRequireBenchmarkGroupsRejectsMissingResult(t *testing.T) {
	groups := map[string][]*benchfmt.Result{
		"BenchmarkA": {{Name: benchfmt.Name("BenchmarkA")}},
	}
	if err := requireBenchmarkGroups([]string{"BenchmarkA", "BenchmarkB"}, groups); err == nil {
		t.Fatal("missing requested benchmark accepted")
	}
}

func TestMatchingBenchmarksAppliesRunSelection(t *testing.T) {
	got, err := matchingBenchmarks([]string{"BenchmarkA", "BenchmarkB"}, "^BenchmarkA$/^case$")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "BenchmarkA" {
		t.Fatalf("matching benchmarks = %v, want [BenchmarkA]", got)
	}
	if _, err := matchingBenchmarks([]string{"BenchmarkA"}, "["); err == nil {
		t.Fatal("invalid benchmark pattern accepted")
	}
	got, err = matchingBenchmarks([]string{"Benchmark_A"}, "Benchmark[ ]A")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "Benchmark_A" {
		t.Fatalf("rewritten-space match = %v, want [Benchmark_A]", got)
	}
}

func TestRestrictBenchmarkPatternPreservesSubBenchmarkSelection(t *testing.T) {
	got, err := restrictBenchmarkPattern("^BenchmarkA$/^one$|^BenchmarkB$/^two$", []string{"BenchmarkA"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "^(?:BenchmarkA)$/^one$" {
		t.Fatalf("restricted pattern = %q", got)
	}
}

func TestGitStateCacheSharesOnlyRecordedWritesAcrossModules(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "root.go"), []byte("package root\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	nestedSource := filepath.Join(nested, "nested.go")
	if err := os.WriteFile(nestedSource, []byte("package nested\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	raw, err := gogit.PlainInit(root, false)
	if err != nil {
		t.Fatal(err)
	}
	wt, err := raw.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if err := wt.AddGlob("."); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Commit("initial", &gogit.CommitOptions{Author: &object.Signature{Name: "t", Email: "t@example.invalid", When: time.Unix(1, 0)}}); err != nil {
		t.Fatal(err)
	}

	cache := newGitStateCache()
	rootState, err := cache.state(root)
	if err != nil {
		t.Fatal(err)
	}
	nestedState, err := cache.state(nested)
	if err != nil {
		t.Fatal(err)
	}
	recording := filepath.Join(root, "benchmarks", "BenchmarkRoot.txt")
	if err := os.Mkdir(filepath.Dir(recording), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(recording, []byte("result"), 0o644); err != nil {
		t.Fatal(err)
	}
	cache.recordWrites(rootState.Root(), []string{recording})
	current, err := gitblob.Snapshot(nested)
	if err != nil {
		t.Fatal(err)
	}
	if !nestedState.EqualExceptPaths(current, cache.writtenPaths(nestedState.Root())) {
		t.Fatal("earlier recording write tainted nested-module baseline")
	}
	if err := os.WriteFile(nestedSource, []byte("package nested\n// changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	current, err = gitblob.Snapshot(nested)
	if err != nil {
		t.Fatal(err)
	}
	if nestedState.EqualExceptPaths(current, cache.writtenPaths(nestedState.Root())) {
		t.Fatal("unrecorded source mutation was excluded")
	}
}

func TestRunRunKeepsSharedRepositoryModulesClean(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"go.mod":                "module example.com/root\n\ngo 1.25\n",
		"root_test.go":          "package root\n\nimport \"testing\"\n\nfunc BenchmarkRoot(b *testing.B) {}\n",
		"nested/go.mod":         "module example.com/nested\n\ngo 1.25\n",
		"nested/nested_test.go": "package nested\n\nimport \"testing\"\n\nfunc BenchmarkNested(b *testing.B) {}\n",
		"go.work":               "go 1.25\n\nuse (\n\t.\n\t./nested\n)\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := gogit.PlainInit(root, false)
	if err != nil {
		t.Fatal(err)
	}
	wt, err := raw.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if err := wt.AddGlob("."); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Commit("initial", &gogit.CommitOptions{Author: &object.Signature{Name: "t", Email: "t@example.invalid", When: time.Unix(1, 0)}}); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)

	var out, errOut bytes.Buffer
	err = runRun(&out, &errOut, runConfig{
		benchDir: "results",
		opts:     runpkg.Options{Count: 1, Benchtime: "1x", Bench: "."},
	}, []string{".", "./nested"})
	if err != nil {
		t.Fatalf("runRun: %v\nstdout:\n%s\nstderr:\n%s", err, out.String(), errOut.String())
	}
	recs, err := store.New(filepath.Join(root, "results")).Read("", "BenchmarkNested", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := recs[0].GetConfig("dirty"); got != "false" {
		t.Fatalf("nested module dirty = %q, want false", got)
	}
}

func TestSourceInputsDirtyIncludesIgnoredAndMetadataStableSource(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		".gitignore":     "generated.go\n",
		"go.mod":         "module example.com/buildinputs\n\ngo 1.25\n",
		"source.go":      "package buildinputs\n\nvar Value = 1\n",
		"source_test.go": "package buildinputs\n\nimport \"testing\"\n\nfunc BenchmarkValue(b *testing.B) { _ = Value }\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := gogit.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	wt, err := raw.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if err := wt.AddGlob("."); err != nil {
		t.Fatal(err)
	}
	commit, err := wt.Commit("initial", &gogit.CommitOptions{Author: &object.Signature{Name: "t", Email: "t@example.invalid", When: time.Unix(1, 0)}})
	if err != nil {
		t.Fatal(err)
	}
	engine, err := gofresh.New(gofresh.WithDir(dir))
	if err != nil {
		t.Fatal(err)
	}
	view, err := engine.NewView([]gofresh.Subject{{Package: "example.com/buildinputs", Symbol: "BenchmarkValue"}}, dir)
	if err != nil {
		t.Fatal(err)
	}
	if dirty, err := sourceInputsDirty(dir, commit.String(), view.SourceFiles()); err != nil || dirty {
		t.Fatalf("committed build inputs: dirty=%v err=%v", dirty, err)
	}

	source := filepath.Join(dir, "source.go")
	info, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("package buildinputs\n\nvar Value = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(source, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if dirty, err := sourceInputsDirty(dir, commit.String(), view.SourceFiles()); err != nil || !dirty {
		t.Fatalf("metadata-stable source change: dirty=%v err=%v", dirty, err)
	}
	if err := os.WriteFile(source, []byte(files["source.go"]), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "generated.go"), []byte("package buildinputs\n\nfunc init() { Value = 2 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	view, err = engine.NewView([]gofresh.Subject{{Package: "example.com/buildinputs", Symbol: "BenchmarkValue"}}, dir)
	if err != nil {
		t.Fatal(err)
	}
	if dirty, err := sourceInputsDirty(dir, commit.String(), view.SourceFiles()); err != nil || !dirty {
		t.Fatalf("ignored selected source: dirty=%v err=%v", dirty, err)
	}
}

func TestRejectRecordingDestinations(t *testing.T) {
	moduleDir := t.TempDir()
	benchDir := filepath.Join(moduleDir, "benchmarks")
	if err := os.Mkdir(benchDir, 0o755); err != nil {
		t.Fatal(err)
	}
	recording := filepath.Join(benchDir, "BenchmarkX.txt")
	if err := os.WriteFile(recording, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	state, err := runtimeinput.FromTestLog([]byte("open benchmarks/BenchmarkX.txt\n"), moduleDir, moduleDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := rejectRecordingDestinations(state, moduleDir, nil, []string{recording}); err == nil {
		t.Fatal("recording destination equal to runtime input accepted")
	}
	directoryState, err := runtimeinput.FromTestLog([]byte("open benchmarks\n"), moduleDir, moduleDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := rejectRecordingDestinations(directoryState, moduleDir, nil, []string{recording}); err == nil {
		t.Fatal("recording destination beneath runtime input directory accepted")
	}
	external := filepath.Join(t.TempDir(), "external.txt")
	if err := os.WriteFile(external, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	externalState, err := runtimeinput.FromTestLog([]byte("open "+external+"\n"), moduleDir, moduleDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := rejectRecordingDestinations(externalState, moduleDir, nil, []string{external}); err == nil {
		t.Fatal("external recording destination equal to runtime input accepted")
	}
	statState, err := runtimeinput.FromTestLog([]byte("stat benchmarks/BenchmarkX.txt\n"), moduleDir, moduleDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := rejectRecordingDestinations(statState, moduleDir, nil, []string{recording}); err == nil {
		t.Fatal("recording destination equal to stat input accepted")
	}
	missingExternal := t.TempDir()
	if err := os.Symlink(missingExternal, filepath.Join(moduleDir, "linked")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	missingState, err := runtimeinput.FromTestLog([]byte("open linked/missing.txt\n"), moduleDir, moduleDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := rejectRecordingDestinations(missingState, moduleDir, nil, []string{filepath.Join(missingExternal, "missing.txt")}); err == nil {
		t.Fatal("missing runtime input beneath symlinked directory accepted")
	}
	if err := rejectRecordingDestinations(state, moduleDir, []string{recording}, []string{recording}); err == nil {
		t.Fatal("recording destination equal to source input accepted")
	}
	aliasRoot := filepath.Join(t.TempDir(), "module-link")
	if err := os.Symlink(moduleDir, aliasRoot); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	aliasRecording := filepath.Join(aliasRoot, "benchmarks", "BenchmarkX.txt")
	if err := rejectRecordingDestinations(state, moduleDir, []string{recording}, []string{aliasRecording}); err == nil {
		t.Fatal("recording destination alias of source input accepted")
	}
	if err := rejectRecordingDestinations(state, moduleDir, nil, []string{filepath.Join(benchDir, "BenchmarkY.txt")}); err != nil {
		t.Fatalf("disjoint recording destination rejected: %v", err)
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
