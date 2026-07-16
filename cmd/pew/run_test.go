package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/pew/internal/gitblob"
	runpkg "github.com/greatliontech/pew/internal/run"
	"github.com/greatliontech/pew/internal/store"
	"golang.org/x/perf/benchfmt"
)

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

func TestRunRecordsIncompleteRuntimeEvidence(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":        "module example.com/incompleterun\n\ngo 1.26.4\n",
		"bench_test.go": "package incompleterun\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc BenchmarkNoIO(b *testing.B) {}\nfunc BenchmarkReadError(b *testing.B) { _, _ = os.ReadFile(\"transiently-missing.txt\") }\n",
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
	if _, err := wt.Commit("initial", &gogit.CommitOptions{Author: &object.Signature{Name: "t", Email: "t@example.invalid", When: time.Unix(1, 0)}}); err != nil {
		t.Fatal(err)
	}
	benchDir := filepath.Join(t.TempDir(), "benchmarks")
	withWorkingDir(t, dir)

	var out, errOut bytes.Buffer
	err = runRun(&out, &errOut, runConfig{
		benchDir: benchDir,
		opts:     runpkg.Options{Count: 1, Benchtime: "1x", Bench: "."},
	}, []string{"."})
	if err != nil {
		t.Fatalf("runRun: %v\nstdout:\n%s\nstderr:\n%s", err, out.String(), errOut.String())
	}
	e, err := newEngine(dir)
	if err != nil {
		t.Fatal(err)
	}
	st := store.New(benchDir)
	for _, bench := range []string{"BenchmarkNoIO", "BenchmarkReadError"} {
		recs, err := st.Read("", bench, "")
		if err != nil {
			t.Fatal(err)
		}
		fp, _, ok := fingerprintFromConfig(recs[0].Config)
		if !ok {
			t.Fatalf("%s recording lacks current format", bench)
		}
		if fp.RuntimeInputs == "" || fp.RuntimeDigest == "" {
			t.Fatalf("%s missing incomplete runtime evidence", bench)
		}
		v, reason, err := checkOne(st, e, "example.com/incompleterun", "", dir, bench, "")
		if err != nil {
			t.Fatal(err)
		}
		if v != verdictUnverifiable || reason != "testlog lacks operation outcome evidence" {
			t.Fatalf("%s recording = {%s %q}, want unverifiable incomplete observation", bench, v, reason)
		}
	}
}

func TestRunStaleIntersectsBenchmarkPattern(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":        "module example.com/stalefilter\n\ngo 1.26.4\n",
		"bench_test.go": "package stalefilter\n\nimport \"testing\"\n\nfunc BenchmarkSelected(b *testing.B) {}\nfunc BenchmarkExcluded(b *testing.B) {}\n",
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
	if _, err := wt.Commit("initial", &gogit.CommitOptions{Author: &object.Signature{Name: "t", Email: "t@example.invalid", When: time.Unix(1, 0)}}); err != nil {
		t.Fatal(err)
	}
	benchDir := filepath.Join(t.TempDir(), "benchmarks")
	withWorkingDir(t, dir)
	var out, errOut bytes.Buffer
	err = runRun(&out, &errOut, runConfig{
		benchDir:  benchDir,
		staleOnly: true,
		opts:      runpkg.Options{Count: 1, Benchtime: "1x", Bench: "^BenchmarkSelected$"},
	}, []string{"."})
	if err != nil {
		t.Fatalf("runRun: %v\nstdout:\n%s\nstderr:\n%s", err, out.String(), errOut.String())
	}
	st := store.New(benchDir)
	if _, err := st.Read("", "BenchmarkSelected", ""); err != nil {
		t.Fatalf("selected benchmark not recorded: %v", err)
	}
	if _, err := st.Read("", "BenchmarkExcluded", ""); !errors.Is(err, store.ErrNotRecorded) {
		t.Fatalf("excluded benchmark read error = %v, want not recorded", err)
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
	if err := rejectRecordingDestinations([]string{recording}, []string{recording}); err == nil {
		t.Fatal("recording destination equal to source input accepted")
	}
	aliasRoot := filepath.Join(t.TempDir(), "module-link")
	if err := os.Symlink(moduleDir, aliasRoot); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	aliasRecording := filepath.Join(aliasRoot, "benchmarks", "BenchmarkX.txt")
	if err := rejectRecordingDestinations([]string{recording}, []string{aliasRecording}); err == nil {
		t.Fatal("recording destination alias of source input accepted")
	}
	if err := rejectRecordingDestinations([]string{recording}, []string{filepath.Join(benchDir, "BenchmarkY.txt")}); err != nil {
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
