package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/thegrumpylion/pew/internal/store"
	"golang.org/x/perf/benchfmt"
)

func writeRecording(t *testing.T, st *store.Store, pkgRel, bench, label string) string {
	t.Helper()
	recs := []*benchfmt.Result{{Name: benchfmt.Name(bench), Iters: 1, Values: []benchfmt.Value{{Value: 1, Unit: "sec/op"}}}}
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
	protected := writeRecording(t, st, "internal/broken", "BenchmarkMaybe", "")
	ignored := filepath.Join(st.Root, "README.txt")
	if err := os.WriteFile(ignored, []byte("not a pew recording"), 0o644); err != nil {
		t.Fatal(err)
	}

	removed, err := gcStore(st, map[string]map[string]bool{
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
	assertPathExists(t, live)
	assertPathExists(t, liveLabel)
	assertPathExists(t, hiddenLabel)
	assertPathExists(t, protected)
	assertPathExists(t, ignored)
	assertPathMissing(t, deadBench)
	assertPathMissing(t, deadLabel)
	assertPathMissing(t, deadPkg)
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
	content := []byte("package p\n\nimport \"testing\"\n\ntype MyB = testing.B\n\nfunc BenchmarkGone() {}\n\nfunc Benchmarkgone(b *testing.B) {}\n\nfunc BenchmarkGeneric[T any](b *testing.B) {}\n\nfunc BenchmarkMyAlias(b *MyB) {}\n")
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
	for _, name := range []string{"BenchmarkGone", "Benchmarkgone", "BenchmarkGeneric", "BenchmarkMyAlias"} {
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
	if err := addStoreOnlySourceBenchmarks(st, moduleDir, live, protected); err != nil {
		t.Fatalf("addStoreOnlySourceBenchmarks: %v", err)
	}
	if protected["internal/tagged"] || !live["internal/tagged"]["BenchmarkTagged"] {
		t.Fatalf("live=%v protected=%v, want tagged benchmark live", live, protected)
	}
	if _, err := gcStore(st, live, protected); err != nil {
		t.Fatalf("gcStore: %v", err)
	}
	assertPathExists(t, kept)
	assertPathMissing(t, removed)
}

func TestGCStoreKeepsRootPackageRecording(t *testing.T) {
	st := store.New(t.TempDir())
	live := writeRecording(t, st, "", "BenchmarkRoot", "")
	dead := writeRecording(t, st, "", "BenchmarkOldRoot", "")

	removed, err := gcStore(st, map[string]map[string]bool{"": {"BenchmarkRoot": true}}, nil)
	if err != nil {
		t.Fatalf("gcStore: %v", err)
	}
	if len(removed) != 1 || removed[0] != "BenchmarkOldRoot" {
		t.Fatalf("removed = %v, want [BenchmarkOldRoot]", removed)
	}
	assertPathExists(t, live)
	assertPathMissing(t, dead)
}
