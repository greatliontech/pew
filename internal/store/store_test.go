package store

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"golang.org/x/perf/benchfmt"
)

const sample = `goos: linux
goarch: amd64
pkg: example.com/x/internal/foo
cpu: TestCPU
commit: abc123def
toolchain: go1.26.4
machine: m-deadbeef
buildconfig: default
dirty: false
pew-closure: 1234abcd5678
pew-runtime: abcdef1234567890
pew-runtime-inputs: eyJ2IjoxfQ
BenchmarkRun-8 1000000 1234 ns/op 456 B/op 7 allocs/op
BenchmarkRun-8 1000000 1240 ns/op 456 B/op 7 allocs/op
BenchmarkRun/case=big-8 200000 6100 ns/op 2048 B/op 9 allocs/op
`

func parse(t *testing.T, raw string) []*benchfmt.Result {
	t.Helper()
	r := benchfmt.NewReader(strings.NewReader(raw), "test")
	var out []*benchfmt.Result
	for r.Scan() {
		switch rec := r.Result().(type) {
		case *benchfmt.Result:
			out = append(out, rec.Clone())
		case *benchfmt.SyntaxError:
			t.Fatalf("sample is not valid benchmark format: %v", rec)
		}
	}
	if err := r.Err(); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("parsed no results")
	}
	return out
}

func configMap(r *benchfmt.Result) map[string]string {
	m := map[string]string{}
	for _, c := range r.Config {
		m[c.Key] = string(c.Value)
	}
	return m
}

func valueMap(r *benchfmt.Result) map[string]float64 {
	m := map[string]float64{}
	for _, v := range r.Values {
		m[v.Unit] = v.Value
	}
	return m
}

func sameResults(t *testing.T, a, b []*benchfmt.Result) {
	t.Helper()
	if len(a) != len(b) {
		t.Fatalf("result count: got %d, want %d", len(b), len(a))
	}
	for i := range a {
		if string(a[i].Name) != string(b[i].Name) {
			t.Errorf("result %d name: got %q, want %q", i, b[i].Name, a[i].Name)
		}
		if a[i].Iters != b[i].Iters {
			t.Errorf("result %d iters: got %d, want %d", i, b[i].Iters, a[i].Iters)
		}
		if !reflect.DeepEqual(configMap(a[i]), configMap(b[i])) {
			t.Errorf("result %d config: got %v, want %v", i, configMap(b[i]), configMap(a[i]))
		}
		if !reflect.DeepEqual(valueMap(a[i]), valueMap(b[i])) {
			t.Errorf("result %d values: got %v, want %v", i, valueMap(b[i]), valueMap(a[i]))
		}
	}
}

func TestWriteBatchPreflightPreservesPriorRecordings(t *testing.T) {
	s := New(t.TempDir())
	oldA := parse(t, "BenchmarkA 1 1 ns/op\n")
	newA := parse(t, "BenchmarkA 1 2 ns/op\n")
	newB := parse(t, "BenchmarkB 1 3 ns/op\n")
	if err := s.Write("p", "BenchmarkA", "", oldA); err != nil {
		t.Fatal(err)
	}
	bPath, err := s.Path("p", "BenchmarkB", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(bPath, 0o755); err != nil {
		t.Fatal(err)
	}
	err = s.WriteBatch([]WriteRequest{
		{PkgRel: "p", Bench: "BenchmarkA", Results: newA},
		{PkgRel: "p", Bench: "BenchmarkB", Results: newB},
	})
	if err == nil {
		t.Fatal("WriteBatch accepted a non-file destination")
	}
	got, err := s.Read("p", "BenchmarkA", "")
	if err != nil {
		t.Fatal(err)
	}
	sameResults(t, got, oldA)
}

func TestWriteBatchRejectsDuplicateDestination(t *testing.T) {
	s := New(t.TempDir())
	err := s.WriteBatch([]WriteRequest{
		{PkgRel: "p", Bench: "BenchmarkA", Results: parse(t, "BenchmarkA 1 1 ns/op\n")},
		{PkgRel: "p", Bench: "BenchmarkA", Results: parse(t, "BenchmarkA 1 2 ns/op\n")},
	})
	if err == nil {
		t.Fatal("WriteBatch accepted duplicate destinations")
	}
}

func TestWriteBatchCommitFailureRestoresPriorRecordings(t *testing.T) {
	s := New(t.TempDir())
	oldA := parse(t, "BenchmarkA 1 1 ns/op\n")
	if err := s.Write("p", "BenchmarkA", "", oldA); err != nil {
		t.Fatal(err)
	}
	bPath, err := s.Path("p", "BenchmarkB", "")
	if err != nil {
		t.Fatal(err)
	}
	err = s.writeBatch([]WriteRequest{
		{PkgRel: "p", Bench: "BenchmarkA", Results: parse(t, "BenchmarkA 1 2 ns/op\n")},
		{PkgRel: "p", Bench: "BenchmarkB", Results: parse(t, "BenchmarkB 1 3 ns/op\n")},
	}, func() {
		if err := os.Mkdir(bPath, 0o755); err != nil {
			t.Fatal(err)
		}
	})
	if err == nil {
		t.Fatal("WriteBatch succeeded despite an install failure")
	}
	got, err := s.Read("p", "BenchmarkA", "")
	if err != nil {
		t.Fatal(err)
	}
	sameResults(t, got, oldA)
}

// TestRoundTrip: write parsed results, read them back, and confirm the recording
// is parseable (INV-3) and stable across a second round-trip.
func TestRoundTrip(t *testing.T) {
	s := New(t.TempDir())
	want := parse(t, sample)

	if err := s.Write("internal/foo", "BenchmarkRun", "", want); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := s.Read("internal/foo", "BenchmarkRun", "") // succeeds ⇒ benchfmt-parseable (INV-3)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	// Re-round-trip must be a fixed point (idempotent storage).
	if err := s.Write("internal/foo", "BenchmarkRun", "", got); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	got2, err := s.Read("internal/foo", "BenchmarkRun", "")
	if err != nil {
		t.Fatalf("reread: %v", err)
	}
	sameResults(t, got, got2)
}

// TestProvenanceRoundTrip enforces INV-4: every provenance key written is
// recoverable on read, byte-for-byte.
func TestProvenanceRoundTrip(t *testing.T) {
	s := New(t.TempDir())
	if err := s.Write("internal/foo", "BenchmarkRun", "", parse(t, sample)); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := s.Read("internal/foo", "BenchmarkRun", "")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	cfg := configMap(got[0])
	for k, want := range map[string]string{
		"commit":      "abc123def",
		"toolchain":   "go1.26.4",
		"machine":     "m-deadbeef",
		"buildconfig": "default",
		"dirty":       "false",
		"pew-closure": "1234abcd5678",
	} {
		if cfg[k] != want {
			t.Errorf("provenance %q: got %q, want %q", k, cfg[k], want)
		}
	}
}

func TestPathLayout(t *testing.T) {
	s := New("bench")
	cases := []struct {
		pkgRel, bench, label, want string
	}{
		{"internal/foo", "BenchmarkRun", "", filepath.FromSlash("bench/internal/foo/BenchmarkRun.txt")},
		{"internal/foo", "BenchmarkRun", "cgo", filepath.FromSlash("bench/internal/foo/BenchmarkRun.cgo.txt")},
		{"", "BenchmarkRoot", "", filepath.FromSlash("bench/BenchmarkRoot.txt")},
	}
	for _, c := range cases {
		got, err := s.Path(c.pkgRel, c.bench, c.label)
		if err != nil {
			t.Errorf("Path(%q,%q,%q): %v", c.pkgRel, c.bench, c.label, err)
			continue
		}
		if got != c.want {
			t.Errorf("Path(%q,%q,%q): got %q, want %q", c.pkgRel, c.bench, c.label, got, c.want)
		}
	}
}

func TestRejectsUnsafeNames(t *testing.T) {
	s := New("bench")
	bad := []struct{ pkgRel, bench, label, what string }{
		{"p", "BenchmarkX", "../escape", "traversal label"},
		{"p", "notabench", "", "non-Benchmark name"},
		{"../../etc", "BenchmarkX", "", "traversal pkgRel"},
		{"..", "BenchmarkX", "", "parent pkgRel"},
		{"a/../b", "BenchmarkX", "", "embedded .. pkgRel"},
		{"/abs", "BenchmarkX", "", "absolute pkgRel"},
		{"a//b", "BenchmarkX", "", "empty segment pkgRel"},
	}
	for _, c := range bad {
		if _, err := s.Path(c.pkgRel, c.bench, c.label); err == nil {
			t.Errorf("expected error for %s (%q,%q,%q)", c.what, c.pkgRel, c.bench, c.label)
		}
	}
}

func TestWriteRejectsSymlinkDirectoryComponent(t *testing.T) {
	root := filepath.Join(t.TempDir(), "benchmarks")
	outside := t.TempDir()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "internal")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	s := New(root)
	if err := s.Write("internal/foo", "BenchmarkRun", "", parse(t, sample)); err == nil {
		t.Fatal("Write through symlink directory succeeded")
	}
	if _, err := os.Stat(filepath.Join(outside, "foo", "BenchmarkRun.txt")); !os.IsNotExist(err) {
		t.Fatalf("Write created outside recording: %v", err)
	}
}

func TestReadRejectsSymlinkBoundary(t *testing.T) {
	root := filepath.Join(t.TempDir(), "benchmarks")
	outside := t.TempDir()
	outsideDir := filepath.Join(outside, "foo")
	if err := os.MkdirAll(outsideDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outsideDir, "BenchmarkRun.txt"), []byte(sample), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "internal")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	s := New(root)
	if _, err := s.Read("internal/foo", "BenchmarkRun", ""); err == nil {
		t.Fatal("Read through symlink directory succeeded")
	}
}

func TestReadRejectsSymlinkRecordingLeaf(t *testing.T) {
	s := New(t.TempDir())
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte(sample), 0o644); err != nil {
		t.Fatal(err)
	}
	path, err := s.Path("internal/foo", "BenchmarkRun", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	if _, err := s.Read("internal/foo", "BenchmarkRun", ""); err == nil {
		t.Fatal("Read of symlink recording leaf succeeded")
	}
}

func TestRemoveRejectsSymlinkBoundary(t *testing.T) {
	root := filepath.Join(t.TempDir(), "benchmarks")
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "foo", "BenchmarkRun.txt")
	if err := os.MkdirAll(filepath.Dir(outsideFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outsideFile, []byte(sample), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "internal")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	s := New(root)
	err := s.Remove(Recording{PkgRel: "internal/foo", Bench: "BenchmarkRun"})
	if err == nil {
		t.Fatal("Remove through symlink directory succeeded")
	}
	if _, err := os.Stat(outsideFile); err != nil {
		t.Fatalf("outside recording was removed: %v", err)
	}
}

func writeRaw(t *testing.T, s *Store, pkgRel, bench, content string) {
	t.Helper()
	p, err := s.Path(pkgRel, bench, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestWriteRejectsEmpty: an empty results slice must error and must NOT clobber
// a prior good recording (M1).
func TestWriteRejectsEmpty(t *testing.T) {
	s := New(t.TempDir())
	want := parse(t, sample)
	if err := s.Write("internal/foo", "BenchmarkRun", "", want); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := s.Write("internal/foo", "BenchmarkRun", "", nil); err == nil {
		t.Error("expected error writing empty results")
	}
	got, err := s.Read("internal/foo", "BenchmarkRun", "")
	if err != nil {
		t.Fatalf("read after rejected empty write: %v", err)
	}
	sameResults(t, want, got)
}

// TestReadEmptyRecording: an existing file with no result lines is reported, not
// returned as (nil, nil) (L1).
func TestReadEmptyRecording(t *testing.T) {
	s := New(t.TempDir())
	writeRaw(t, s, "internal/foo", "BenchmarkRun", "goos: linux\ncommit: abc\n")
	if _, err := s.Read("internal/foo", "BenchmarkRun", ""); err == nil {
		t.Error("expected error reading result-less recording")
	}
}

// TestReadRejectsUnitMetadata: a Unit-metadata record must be surfaced, never
// silently dropped (H2).
func TestReadRejectsUnitMetadata(t *testing.T) {
	s := New(t.TempDir())
	writeRaw(t, s, "internal/foo", "BenchmarkRun", "Unit ns/op better=lower\nBenchmarkRun-8 10 5 ns/op\n")
	if _, err := s.Read("internal/foo", "BenchmarkRun", ""); err == nil {
		t.Error("expected error: Unit-metadata record must not be silently dropped")
	}
}

// TestParseFromContent: Parse (the entry point for git-blob baselines, §10) reads
// in-memory benchmark-format content equivalently to Read, and surfaces malformed
// or empty content rather than silently dropping it.
func TestParseFromContent(t *testing.T) {
	got, err := Parse(strings.NewReader(sample), "HEAD:bench.txt")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	sameResults(t, parse(t, sample), got)

	if _, err := Parse(strings.NewReader("goos: linux\n"), "blob"); err == nil {
		t.Error("Parse of result-less content: want error")
	}
	if _, err := Parse(strings.NewReader("BenchmarkRun notAnInt ns/op\n"), "blob"); err == nil {
		t.Error("Parse of corrupt content: want error")
	}
}

func TestReadNotRecorded(t *testing.T) {
	s := New(t.TempDir())
	if _, err := s.Read("internal/foo", "BenchmarkMissing", ""); err != ErrNotRecorded {
		t.Errorf("got %v, want ErrNotRecorded", err)
	}
}

// TestReadCorrupt: a malformed recording (hand-edit, merge conflict, truncation)
// must be reported, never silently read as empty/valid.
func TestReadCorrupt(t *testing.T) {
	s := New(t.TempDir())
	p, err := s.Path("internal/foo", "BenchmarkRun", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("BenchmarkRun notAnInt ns/op\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Read("internal/foo", "BenchmarkRun", ""); err == nil {
		t.Error("expected error reading corrupt recording, got nil")
	}
}

func TestListRecordingsIgnoresNonStoreFiles(t *testing.T) {
	s := New(t.TempDir())
	if err := s.Write("internal/foo", "BenchmarkRun", "", parse(t, sample)); err != nil {
		t.Fatalf("write unlabeled: %v", err)
	}
	if err := s.Write("internal/foo", "BenchmarkRun", "cgo", parse(t, sample)); err != nil {
		t.Fatalf("write labeled: %v", err)
	}
	if err := os.WriteFile(filepath.Join(s.Root, "README.txt"), []byte("not a recording"), 0o644); err != nil {
		t.Fatal(err)
	}
	badDir := filepath.Join(s.Root, "internal", "foo")
	if err := os.WriteFile(filepath.Join(badDir, "notabench.txt"), []byte("ignored"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte(sample), 0o644); err != nil {
		t.Fatal(err)
	}
	symlinkPath := filepath.Join(badDir, "BenchmarkSymlink.txt")
	if err := os.Symlink(outside, symlinkPath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	outsideDir := filepath.Join(t.TempDir(), "outside-dir")
	if err := os.MkdirAll(filepath.Join(outsideDir, "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outsideDir, "pkg", "BenchmarkOutside.txt"), []byte(sample), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideDir, filepath.Join(s.Root, "linked")); err != nil {
		t.Skipf("directory symlink unavailable: %v", err)
	}

	recs, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	var got []string
	for _, r := range recs {
		got = append(got, r.PkgRel+"/"+r.Bench+"/"+r.Label)
	}
	sort.Strings(got)
	want := []string{"internal/foo/BenchmarkRun/", "internal/foo/BenchmarkRun/cgo"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("List = %v, want %v", got, want)
	}
}

func TestRemoveRecordingPrunesEmptyDirs(t *testing.T) {
	s := New(t.TempDir())
	if err := s.Write("internal/foo", "BenchmarkRun", "", parse(t, sample)); err != nil {
		t.Fatalf("write: %v", err)
	}
	recs, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("recordings = %d, want 1", len(recs))
	}
	if err := s.Remove(recs[0]); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(recs[0].Path); !os.IsNotExist(err) {
		t.Fatalf("removed file still exists: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.Root, "internal")); !os.IsNotExist(err) {
		t.Fatalf("empty package dirs were not pruned: %v", err)
	}
}
