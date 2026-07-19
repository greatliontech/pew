package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	runpkg "github.com/greatliontech/pew/internal/run"
	"github.com/greatliontech/pew/internal/store"
	"golang.org/x/perf/benchfmt"
)

func writeRecording(t *testing.T, st *store.Store, pkgRel, bench, label string) string {
	t.Helper()
	recs := []*benchfmt.Result{{
		Name:   benchfmt.Name(bench),
		Iters:  1,
		Values: []benchfmt.Value{{Value: 1, Unit: "sec/op"}},
		Config: []benchfmt.Config{
			{Key: "pew-format", Value: []byte(runpkg.RecordingFormat), File: true},
			{Key: "commit", Value: []byte("c1"), File: true},
			{Key: "toolchain", Value: []byte("go-test"), File: true},
			{Key: "machine", Value: []byte("m1"), File: true},
			{Key: "buildconfig", Value: []byte("b1"), File: true},
			{Key: "runtimeconfig", Value: []byte("r1"), File: true},
			{Key: "dirty", Value: []byte("false"), File: true},
			{Key: "pew-runconditions", Value: []byte("governor=performance turbo=off load1=0.03 throttled=false battery=false"), File: true},
			{Key: "pew-closure", Value: []byte("cl1"), File: true},
			{Key: "pew-runtime", Value: []byte("rt1"), File: true},
			{Key: "pew-runtime-inputs", Value: []byte("manifest1"), File: true},
		},
	}}
	if err := st.Write(pkgRel, bench, label, recs); err != nil {
		t.Fatalf("Write(%q,%q,%q): %v", pkgRel, bench, label, err)
	}
	path, err := st.Path(pkgRel, bench, label)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func assertPathExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("%s: expected to exist: %v", path, err)
	}
}

func assertPathMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("%s: expected missing, got %v", path, err)
	}
}

func TestGCStoreRemovesOnlyMissingBenchmarks(t *testing.T) {
	st := store.New(t.TempDir())
	live := writeRecording(t, st, "internal/foo", "BenchmarkLive", "")
	liveLabel := writeRecording(t, st, "internal/foo", "BenchmarkLive", "x")
	deadBench := writeRecording(t, st, "internal/foo", "BenchmarkGone", "")
	hiddenLabel := writeRecording(t, st, "internal/foo", "BenchmarkTagged", "exp")
	deadLabel := writeRecording(t, st, "internal/foo", "BenchmarkDeleted", "exp")
	deadPkg := writeRecording(t, st, "internal/old", "BenchmarkOld", "")
	deadPkgData, err := os.ReadFile(deadPkg)
	if err != nil {
		t.Fatal(err)
	}
	deadPkgData = bytes.Replace(deadPkgData, []byte("pew-format: 1\n"), nil, 1)
	if err := os.WriteFile(deadPkg, deadPkgData, 0o644); err != nil {
		t.Fatal(err)
	}
	protected := writeRecording(t, st, "internal/broken", "BenchmarkMaybe", "")
	ignored := filepath.Join(st.Root, "README.txt")
	if err := os.WriteFile(ignored, []byte("not a pew recording"), 0o644); err != nil {
		t.Fatal(err)
	}
	pathShaped, err := st.Path("internal/notes", "BenchmarkNotes", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(pathShaped), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pathShaped, []byte("BenchmarkNotes-8 1 1 ns/op\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	removed, kept, err := gcStore(st, map[string]map[string]bool{
		"internal/foo": {"BenchmarkLive": true, "BenchmarkTagged": true},
	}, map[string]bool{"internal/broken": true})
	if err != nil {
		t.Fatalf("gcStore: %v", err)
	}
	sort.Strings(removed)
	want := []string{"internal/foo.BenchmarkDeleted.exp", "internal/foo.BenchmarkGone", "internal/old.BenchmarkOld"}
	if !reflect.DeepEqual(removed, want) {
		t.Fatalf("removed = %v, want %v", removed, want)
	}
	if len(kept) != 0 {
		t.Fatalf("kept reports = %v, want none (no live stale-format or unreadable recordings)", kept)
	}
	assertPathExists(t, live)
	assertPathExists(t, liveLabel)
	assertPathExists(t, hiddenLabel)
	assertPathExists(t, protected)
	assertPathExists(t, ignored)
	assertPathExists(t, pathShaped)
	assertPathMissing(t, deadBench)
	assertPathMissing(t, deadLabel)
	assertPathMissing(t, deadPkg)
}

// TestGCStoreSurfacesShapeFailingRecordings pins the spec §12 gc contract for
// recordings outside the current format: an old-shape pew recording whose
// benchmark is gone is removed like any orphan; one whose benchmark still
// exists is kept and reported stale (format); an unreadable file is kept and
// reported with its error; a foreign layout-matching file (no pew marker)
// stays silently ignored.
func TestGCStoreSurfacesShapeFailingRecordings(t *testing.T) {
	st := store.New(t.TempDir())
	stripKey := func(path, key string) {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		lines := bytes.Split(data, []byte{'\n'})
		out := lines[:0]
		for _, l := range lines {
			if !bytes.HasPrefix(l, []byte(key+":")) {
				out = append(out, l)
			}
		}
		if err := os.WriteFile(path, bytes.Join(out, []byte{'\n'}), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Old-shape (missing the mandatory pew-runconditions field), benchmark gone.
	orphanOld := writeRecording(t, st, "internal/p", "BenchmarkGoneOld", "")
	stripKey(orphanOld, "pew-runconditions")
	// Old-shape, benchmark still in source.
	liveOld := writeRecording(t, st, "internal/p", "BenchmarkLiveOld", "")
	stripKey(liveOld, "pew-runconditions")
	// Unreadable: config lines only, no result rows — Read fails.
	unreadable, err := st.Path("internal/p", "BenchmarkBroken", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unreadable, []byte("pew-format: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Foreign: parseable benchmark file with no pew-owned key, benchmark gone.
	foreign, err := st.Path("internal/p", "BenchmarkForeign", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(foreign, []byte("BenchmarkForeign-8 1 1 ns/op\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	removed, kept, err := gcStore(st, map[string]map[string]bool{
		"internal/p": {"BenchmarkLiveOld": true},
	}, nil)
	if err != nil {
		t.Fatalf("gcStore: %v", err)
	}
	if !reflect.DeepEqual(removed, []string{"internal/p.BenchmarkGoneOld"}) {
		t.Fatalf("removed = %v, want [internal/p.BenchmarkGoneOld]", removed)
	}
	sort.Strings(kept)
	if len(kept) != 2 {
		t.Fatalf("kept reports = %v, want unreadable + stale-format", kept)
	}
	if !strings.Contains(kept[0], "error") || !strings.Contains(kept[0], "internal/p.BenchmarkBroken") {
		t.Errorf("kept[0] = %q, want unreadable report for BenchmarkBroken", kept[0])
	}
	if !strings.Contains(kept[1], "stale-format") || !strings.Contains(kept[1], "internal/p.BenchmarkLiveOld") || !strings.Contains(kept[1], "re-run `pew run`") {
		t.Errorf("kept[1] = %q, want stale-format report for BenchmarkLiveOld", kept[1])
	}
	assertPathMissing(t, orphanOld)
	assertPathExists(t, liveOld)
	assertPathExists(t, unreadable)
	assertPathExists(t, foreign)
}

// TestRunGCRemovesOldShapeOrphanInStoreOnlyPackage drives the whole-command
// path over the case the store-only scan used to hide: a package directory
// deleted from source whose only recording predates a mandatory field. It
// must be removed, not silently skipped forever.
func TestRunGCRemovesOldShapeOrphanInStoreOnlyPackage(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/gc\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st := store.New(filepath.Join(dir, "benchmarks"))
	orphan := writeRecording(t, st, "internal/gone", "BenchmarkOld", "")
	data, err := os.ReadFile(orphan)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(orphan, bytes.ReplaceAll(data, []byte("pew-runtime-inputs: manifest1\n"), nil), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	var out bytes.Buffer
	if err := runGC(&out, st.Root); err != nil {
		t.Fatalf("runGC: %v\n%s", err, out.String())
	}
	assertPathMissing(t, orphan)
	if !strings.Contains(out.String(), "removed      internal/gone.BenchmarkOld") {
		t.Fatalf("output = %q, want old-shape orphan removed", out.String())
	}
}

// TestRunGCKeepsOldShapeRecordingInStoreOnlyPackage pins the source-scan
// anchor: a package invisible to the ./... resolution (underscore dir) whose
// benchmark exists behind a build tag, with an old-shape recording. Without
// the shape-failing recording anchoring the package's source scan, the live
// benchmark's recording would look orphaned and be deleted; it must instead
// be kept and reported stale (format).
func TestRunGCKeepsOldShapeRecordingInStoreOnlyPackage(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/gc\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "root.go"), []byte("package root\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pkgDir := filepath.Join(dir, "internal", "_tagged")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	tagged := []byte("//go:build exp\n\npackage tagged\n\nimport \"testing\"\n\nfunc BenchmarkTagged(b *testing.B) {}\n")
	if err := os.WriteFile(filepath.Join(pkgDir, "tag_bench_test.go"), tagged, 0o644); err != nil {
		t.Fatal(err)
	}

	st := store.New(filepath.Join(dir, "benchmarks"))
	kept := writeRecording(t, st, "internal/_tagged", "BenchmarkTagged", "exp")
	data, err := os.ReadFile(kept)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(kept, bytes.ReplaceAll(data, []byte("pew-runconditions: governor=performance turbo=off load1=0.03 throttled=false battery=false\n"), nil), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	var out bytes.Buffer
	if err := runGC(&out, st.Root); err != nil {
		t.Fatalf("runGC: %v\n%s", err, out.String())
	}
	assertPathExists(t, kept)
	if !strings.Contains(out.String(), "stale-format") || !strings.Contains(out.String(), "internal/_tagged.BenchmarkTagged.exp") {
		t.Fatalf("output = %q, want stale-format report for the kept recording", out.String())
	}
	if strings.Contains(out.String(), "gc: no stale recordings") {
		t.Fatalf("output = %q, must not claim a clean store beside a kept report", out.String())
	}
}

// TestRunGCReportsScanErrorForStoreOnlyPackage pins spec §12's never-silent
// protection: a store-only package whose benchmark-source scan fails (a
// syntax-error test file) keeps its recordings behind a reported error line —
// not behind silence, and not behind "gc: no stale recordings".
func TestRunGCReportsScanErrorForStoreOnlyPackage(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/gc\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "root.go"), []byte("package root\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pkgDir := filepath.Join(dir, "internal", "_broken")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "x_test.go"), []byte("package broken\n\nfunc {\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st := store.New(filepath.Join(dir, "benchmarks"))
	kept := writeRecording(t, st, "internal/_broken", "BenchmarkX", "")
	t.Chdir(dir)

	var out bytes.Buffer
	if err := runGC(&out, st.Root); err != nil {
		t.Fatalf("runGC: %v\n%s", err, out.String())
	}
	assertPathExists(t, kept)
	if !strings.Contains(out.String(), "error") || !strings.Contains(out.String(), "internal/_broken") {
		t.Fatalf("output = %q, want a reported scan error for internal/_broken", out.String())
	}
	if strings.Contains(out.String(), "gc: no stale recordings") {
		t.Fatalf("output = %q, must not claim a clean store beside a reported error", out.String())
	}
}

func TestSourceBenchmarksIncludesBuildTaggedFiles(t *testing.T) {
	dir := t.TempDir()
	content := []byte("//go:build exp\n\npackage p\n\nimport \"testing\"\n\nfunc BenchmarkTagged(b *testing.B) {}\n")
	if err := os.WriteFile(filepath.Join(dir, "tag_bench_test.go"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	benches, exists, err := sourceBenchmarks(dir)
	if err != nil {
		t.Fatalf("sourceBenchmarks: %v", err)
	}
	if !exists || !benches["BenchmarkTagged"] {
		t.Fatalf("sourceBenchmarks = %v exists=%v, want BenchmarkTagged", benches, exists)
	}
}

func TestSelectedBenchmarksUsesBuildSelectedTestFiles(t *testing.T) {
	dir := t.TempDir()
	selected := []byte("package p\n\nimport \"testing\"\n\nfunc BenchmarkSelected(b *testing.B) {}\n")
	if err := os.WriteFile(filepath.Join(dir, "selected_test.go"), selected, 0o644); err != nil {
		t.Fatal(err)
	}
	hidden := []byte("package p\n\nimport \"testing\"\n\nfunc BenchmarkHidden(b *testing.B) {}\n")
	if err := os.WriteFile(filepath.Join(dir, "hidden_test.go"), hidden, 0o644); err != nil {
		t.Fatal(err)
	}
	benches, err := selectedBenchmarks(pkgMeta{Dir: dir, TestGoFiles: []string{"selected_test.go"}})
	if err != nil {
		t.Fatalf("selectedBenchmarks: %v", err)
	}
	want := []string{"BenchmarkSelected"}
	if !reflect.DeepEqual(benches, want) {
		t.Fatalf("selectedBenchmarks = %v, want %v", benches, want)
	}
}

func TestSourceBenchmarksAcceptsTestingBAlias(t *testing.T) {
	dir := t.TempDir()
	content := []byte("package p\n\nimport (\n\t\"testing\"\n\ttb \"example.com/tb\"\n)\n\ntype B = testing.B\n\nfunc BenchmarkAlias(b *B) {}\n\nfunc BenchmarkSelectorAlias(b *tb.B) {}\n")
	if err := os.WriteFile(filepath.Join(dir, "alias_test.go"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	benches, exists, err := sourceBenchmarks(dir)
	if err != nil {
		t.Fatalf("sourceBenchmarks: %v", err)
	}
	if !exists || !benches["BenchmarkAlias"] || !benches["BenchmarkSelectorAlias"] {
		t.Fatalf("sourceBenchmarks = %v exists=%v, want alias benchmarks", benches, exists)
	}
}

func TestSourceBenchmarksIgnoresInvalidBenchmarkSignature(t *testing.T) {
	dir := t.TempDir()
	content := []byte("package p\n\nimport \"testing\"\n\ntype MyB = testing.B\n\nfunc BenchmarkGone() {}\n\nfunc Benchmarkgone(b *testing.B) {}\n\nfunc BenchmarkGeneric[T any](b *testing.B) {}\n\nfunc BenchmarkMyAlias(b *MyB) {}\n\nfunc BenchmarkTwoParams(b, c *testing.B) {}\n")
	if err := os.WriteFile(filepath.Join(dir, "gone_test.go"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	benches, exists, err := sourceBenchmarks(dir)
	if err != nil {
		t.Fatalf("sourceBenchmarks: %v", err)
	}
	if !exists {
		t.Fatal("sourceBenchmarks reported package missing")
	}
	for _, name := range []string{"BenchmarkGone", "Benchmarkgone", "BenchmarkGeneric", "BenchmarkMyAlias", "BenchmarkTwoParams"} {
		if benches[name] {
			t.Fatalf("sourceBenchmarks = %v, want %s ignored", benches, name)
		}
	}
}

func TestRunGCRetainsSelectorAliasBenchmark(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/gc\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tbDir := filepath.Join(dir, "tb")
	if err := os.MkdirAll(tbDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tbDir, "tb.go"), []byte("package tb\n\nimport \"testing\"\n\ntype B = testing.B\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pkgDir := filepath.Join(dir, "internal", "foo")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := []byte("package foo\n\nimport tb \"example.com/gc/tb\"\n\nfunc BenchmarkAlias(b *tb.B) {}\n")
	if err := os.WriteFile(filepath.Join(pkgDir, "alias_test.go"), content, 0o644); err != nil {
		t.Fatal(err)
	}

	st := store.New(filepath.Join(dir, "benchmarks"))
	kept := writeRecording(t, st, "internal/foo", "BenchmarkAlias", "")
	t.Chdir(dir)

	var out bytes.Buffer
	if err := runGC(&out, st.Root); err != nil {
		t.Fatalf("runGC: %v\n%s", err, out.String())
	}
	assertPathExists(t, kept)
	if !strings.Contains(out.String(), "gc: no stale recordings") {
		t.Fatalf("output = %q, want no stale recordings", out.String())
	}
}

func TestRunGCRetainsTestingBAliasBenchmark(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/gc\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pkgDir := filepath.Join(dir, "internal", "foo")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := []byte("package foo\n\nimport \"testing\"\n\ntype B = testing.B\n\nfunc BenchmarkAlias(b *B) {}\n")
	if err := os.WriteFile(filepath.Join(pkgDir, "alias_test.go"), content, 0o644); err != nil {
		t.Fatal(err)
	}

	st := store.New(filepath.Join(dir, "benchmarks"))
	kept := writeRecording(t, st, "internal/foo", "BenchmarkAlias", "")
	t.Chdir(dir)

	var out bytes.Buffer
	if err := runGC(&out, st.Root); err != nil {
		t.Fatalf("runGC: %v\n%s", err, out.String())
	}
	assertPathExists(t, kept)
	if !strings.Contains(out.String(), "gc: no stale recordings") {
		t.Fatalf("output = %q, want no stale recordings", out.String())
	}
}

func TestRunGCRetainsBuildTaggedBenchmarkAndRemovesDeletedLabel(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/gc\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pkgDir := filepath.Join(dir, "internal", "foo")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "foo.go"), []byte("package foo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tagged := []byte("//go:build exp\n\npackage foo\n\nimport \"testing\"\n\nfunc BenchmarkTagged(b *testing.B) {}\n")
	if err := os.WriteFile(filepath.Join(pkgDir, "tag_bench_test.go"), tagged, 0o644); err != nil {
		t.Fatal(err)
	}

	st := store.New(filepath.Join(dir, "benchmarks"))
	kept := writeRecording(t, st, "internal/foo", "BenchmarkTagged", "")
	removed := writeRecording(t, st, "internal/foo", "BenchmarkDeleted", "exp")
	t.Chdir(dir)

	var out bytes.Buffer
	if err := runGC(&out, st.Root); err != nil {
		t.Fatalf("runGC: %v\n%s", err, out.String())
	}
	assertPathExists(t, kept)
	assertPathMissing(t, removed)
	if !strings.Contains(out.String(), "removed      internal/foo.BenchmarkDeleted.exp") {
		t.Fatalf("output = %q, want removed deleted label", out.String())
	}
}

func TestRunGCRemovesInvalidBenchmarkSignature(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/gc\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pkgDir := filepath.Join(dir, "internal", "foo")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "foo.go"), []byte("package foo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "gone_test.go"), []byte("package foo\n\nfunc BenchmarkGone() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	st := store.New(filepath.Join(dir, "benchmarks"))
	removed := writeRecording(t, st, "internal/foo", "BenchmarkGone", "exp")
	t.Chdir(dir)

	var out bytes.Buffer
	if err := runGC(&out, st.Root); err != nil {
		t.Fatalf("runGC: %v\n%s", err, out.String())
	}
	assertPathMissing(t, removed)
	if !strings.Contains(out.String(), "removed      internal/foo.BenchmarkGone.exp") {
		t.Fatalf("output = %q, want removed invalid benchmark", out.String())
	}
}

func TestRunGCProtectsStoreOnlyPackage(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/gc\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "root.go"), []byte("package root\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pkgDir := filepath.Join(dir, "internal", "_tagged")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	tagged := []byte("//go:build exp\n\npackage tagged\n\nimport \"testing\"\n\nfunc BenchmarkTagged(b *testing.B) {}\n")
	if err := os.WriteFile(filepath.Join(pkgDir, "tag_bench_test.go"), tagged, 0o644); err != nil {
		t.Fatal(err)
	}

	st := store.New(filepath.Join(dir, "benchmarks"))
	kept := writeRecording(t, st, "internal/_tagged", "BenchmarkTagged", "exp")
	removed := writeRecording(t, st, "internal/_tagged", "BenchmarkGone", "exp")
	t.Chdir(dir)

	var out bytes.Buffer
	if err := runGC(&out, st.Root); err != nil {
		t.Fatalf("runGC: %v\n%s", err, out.String())
	}
	assertPathExists(t, kept)
	assertPathMissing(t, removed)
	if !strings.Contains(out.String(), "removed      internal/_tagged.BenchmarkGone.exp") {
		t.Fatalf("output = %q, want removed store-only deleted benchmark", out.String())
	}
}

func TestRunGCWithNoListedPackagesRemovesRecordings(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/gc\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st := store.New(filepath.Join(dir, "benchmarks"))
	removed := writeRecording(t, st, "internal/gone", "BenchmarkGone", "")
	t.Chdir(dir)

	var out bytes.Buffer
	if err := runGC(&out, st.Root); err != nil {
		t.Fatalf("runGC: %v\n%s", err, out.String())
	}
	assertPathMissing(t, removed)
	if !strings.Contains(out.String(), "removed      internal/gone.BenchmarkGone") {
		t.Fatalf("output = %q, want removed recording with no listed packages", out.String())
	}
}

func TestStoreOnlySourceBenchmarksProtectLiveTaggedPackage(t *testing.T) {
	moduleDir := t.TempDir()
	pkgDir := filepath.Join(moduleDir, "internal", "tagged")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	tagged := []byte("//go:build exp\n\npackage tagged\n\nimport \"testing\"\n\nfunc BenchmarkTagged(b *testing.B) {}\n")
	if err := os.WriteFile(filepath.Join(pkgDir, "tag_bench_test.go"), tagged, 0o644); err != nil {
		t.Fatal(err)
	}

	st := store.New(filepath.Join(moduleDir, "benchmarks"))
	kept := writeRecording(t, st, "internal/tagged", "BenchmarkTagged", "exp")
	removed := writeRecording(t, st, "internal/tagged", "BenchmarkGone", "exp")
	live := map[string]map[string]bool{}
	protected := map[string]bool{}
	if _, err := addStoreOnlySourceBenchmarks(io.Discard, st, moduleDir, live, protected); err != nil {
		t.Fatalf("addStoreOnlySourceBenchmarks: %v", err)
	}
	if protected["internal/tagged"] || !live["internal/tagged"]["BenchmarkTagged"] {
		t.Fatalf("live=%v protected=%v, want tagged benchmark live", live, protected)
	}
	if _, _, err := gcStore(st, live, protected); err != nil {
		t.Fatalf("gcStore: %v", err)
	}
	assertPathExists(t, kept)
	assertPathMissing(t, removed)
}

func TestGCStoreKeepsRootPackageRecording(t *testing.T) {
	st := store.New(t.TempDir())
	live := writeRecording(t, st, "", "BenchmarkRoot", "")
	dead := writeRecording(t, st, "", "BenchmarkOldRoot", "")

	removed, kept, err := gcStore(st, map[string]map[string]bool{"": {"BenchmarkRoot": true}}, nil)
	if err != nil {
		t.Fatalf("gcStore: %v", err)
	}
	if len(removed) != 1 || removed[0] != "BenchmarkOldRoot" {
		t.Fatalf("removed = %v, want [BenchmarkOldRoot]", removed)
	}
	if len(kept) != 0 {
		t.Fatalf("kept reports = %v, want none", kept)
	}
	assertPathExists(t, live)
	assertPathMissing(t, dead)
}
