package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/runtimeinput"
	"github.com/greatliontech/pew/internal/compare"
	"github.com/greatliontech/pew/internal/gitblob"
	runpkg "github.com/greatliontech/pew/internal/run"
	"github.com/greatliontech/pew/internal/store"
	"golang.org/x/perf/benchfmt"
)

func TestBaselineFor(t *testing.T) {
	tests := []struct {
		refs              []string
		wantBase, wantNew string
		wantErr           bool
	}{
		{nil, "HEAD", "", false},                // auto: working tree vs HEAD
		{[]string{"v1"}, "v1", "", false},       // pinned: working tree vs v1
		{[]string{"a", "b"}, "a", "b", false},   // A/B: a vs b
		{[]string{"a", "b", "c"}, "", "", true}, // too many
	}
	for _, tt := range tests {
		bl, err := baselineFor(tt.refs)
		if (err != nil) != tt.wantErr {
			t.Errorf("baselineFor(%v) err=%v, wantErr=%v", tt.refs, err, tt.wantErr)
			continue
		}
		if err != nil {
			continue
		}
		if bl.baseRef != tt.wantBase || bl.newRef != tt.wantNew {
			t.Errorf("baselineFor(%v) = {%q,%q}, want {%q,%q}", tt.refs, bl.baseRef, bl.newRef, tt.wantBase, tt.wantNew)
		}
	}
}

func TestParseGateUnits(t *testing.T) {
	ok := []struct {
		in   string
		want []string
	}{
		{"sec/op", []string{"sec/op"}},
		{"sec/op,B/op", []string{"sec/op", "B/op"}},
		{"sec/op B/op allocs/op", []string{"sec/op", "B/op", "allocs/op"}},
		{"sec/op, allocs/op", []string{"sec/op", "allocs/op"}},
	}
	for _, tt := range ok {
		got, err := parseGateUnits(tt.in)
		if err != nil {
			t.Errorf("parseGateUnits(%q): %v", tt.in, err)
			continue
		}
		for _, u := range tt.want {
			if !got[u] {
				t.Errorf("parseGateUnits(%q) missing %q", tt.in, u)
			}
		}
		if len(got) != len(tt.want) {
			t.Errorf("parseGateUnits(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}

	for _, bad := range []string{"", "  ", "ns/op", "sec/op,bogus", "cpu"} {
		if _, err := parseGateUnits(bad); err == nil {
			t.Errorf("parseGateUnits(%q): want error", bad)
		}
	}
}

func TestValidateOptions(t *testing.T) {
	base := compare.DefaultOptions()
	if err := validateOptions(base); err != nil {
		t.Errorf("defaults rejected: %v", err)
	}
	zeroThresh := base
	zeroThresh.ThresholdPct = 0 // legitimate: "any significant worse change"
	if err := validateOptions(zeroThresh); err != nil {
		t.Errorf("threshold 0 rejected: %v", err)
	}

	bad := []compare.Options{
		{Alpha: 0, ThresholdPct: 3, Confidence: 0.95},     // alpha too low (would silently pass everything)
		{Alpha: 1, ThresholdPct: 3, Confidence: 0.95},     // alpha too high
		{Alpha: 0.05, ThresholdPct: -1, Confidence: 0.95}, // negative floor guts the magnitude gate
		{Alpha: 0.05, ThresholdPct: 3, Confidence: 0},     // confidence out of range
		{Alpha: 0.05, ThresholdPct: 3, Confidence: 1},
	}
	for _, o := range bad {
		if err := validateOptions(o); err == nil {
			t.Errorf("validateOptions(%+v): want error", o)
		}
	}
}

func TestStatABIncludesHistoricalOnlyRecording(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/statinv\n\ngo 1.26.4\n")
	writeFile(t, filepath.Join(dir, "pkg", "pkg.go"), "package pkg\n")

	repo, err := gogit.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("git init: %v", err)
	}
	st := store.New(filepath.Join(dir, "benchmarks"))
	writeStatRecording(t, st, "pkg", "BenchmarkOld", 100)
	base := commitAll(t, repo, "base")

	writeStatRecording(t, st, "pkg", "BenchmarkOld", 120)
	newer := commitAll(t, repo, "newer")

	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })

	var out, errOut bytes.Buffer
	err = runStat(&out, &errOut, statConfig{benchDir: st.Root, opts: compare.DefaultOptions()}, []string{base.String(), newer.String()})
	if err != nil {
		t.Fatalf("runStat: %v\nstderr:\n%s", err, errOut.String())
	}
	if !strings.Contains(out.String(), "BenchmarkOld") {
		t.Fatalf("stat output omitted historical-only benchmark:\nstdout:\n%s\nstderr:\n%s", out.String(), errOut.String())
	}
}

func TestStatABFallsBackToModuleWithoutCurrentPackages(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/statnopkg\n\ngo 1.26.4\n")

	repo, err := gogit.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("git init: %v", err)
	}
	st := store.New(filepath.Join(dir, "benchmarks"))
	writeStatRecording(t, st, "pkg", "BenchmarkOld", 100)
	base := commitAll(t, repo, "base")
	writeStatRecording(t, st, "pkg", "BenchmarkOld", 120)
	newer := commitAll(t, repo, "newer")

	withWorkingDir(t, dir)
	var out, errOut bytes.Buffer
	err = runStat(&out, &errOut, statConfig{benchDir: st.Root, opts: compare.DefaultOptions()}, []string{base.String(), newer.String()})
	if err != nil {
		t.Fatalf("runStat: %v\nstderr:\n%s", err, errOut.String())
	}
	if !strings.Contains(out.String(), "BenchmarkOld") {
		t.Fatalf("stat output omitted ref-only module recording:\nstdout:\n%s\nstderr:\n%s", out.String(), errOut.String())
	}
}

func TestStatPinnedIncludesWorkingTreeOnlyRecordingWithoutCurrentBenchmark(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/statpinnedworktree\n\ngo 1.26.4\n")

	repo, err := gogit.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("git init: %v", err)
	}
	base := commitAll(t, repo, "base")
	st := store.New(filepath.Join(dir, "benchmarks"))
	writeStatRecording(t, st, "pkg", "BenchmarkNew", 120)

	withWorkingDir(t, dir)
	var out, errOut bytes.Buffer
	err = runStat(&out, &errOut, statConfig{benchDir: st.Root, opts: compare.DefaultOptions()}, []string{base.String()})
	if err != nil {
		t.Fatalf("runStat: %v\nstderr:\n%s", err, errOut.String())
	}
	if !strings.Contains(out.String(), "BenchmarkNew") {
		t.Fatalf("stat output omitted working-tree-only recording:\nstdout:\n%s\nstderr:\n%s", out.String(), errOut.String())
	}
	if !strings.Contains(errOut.String(), "no current benchmark declaration") {
		t.Fatalf("stat stderr omitted missing current declaration warning:\nstdout:\n%s\nstderr:\n%s", out.String(), errOut.String())
	}
}

func TestStatABDiscoversHistoricalModuleAbsentFromCurrentPackages(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/current\n\ngo 1.26.4\n")
	writeFile(t, filepath.Join(dir, "oldmod", "go.mod"), "module example.com/oldmod\n\ngo 1.26.4\n")

	repo, err := gogit.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("git init: %v", err)
	}
	st := store.New(filepath.Join(dir, "oldmod", "benchmarks"))
	writeStatRecording(t, st, "pkg", "BenchmarkOld", 100)
	base := commitAll(t, repo, "base")
	writeStatRecording(t, st, "pkg", "BenchmarkOld", 120)
	newer := commitAll(t, repo, "newer")
	if err := os.RemoveAll(filepath.Join(dir, "oldmod")); err != nil {
		t.Fatal(err)
	}

	withWorkingDir(t, dir)
	var out, errOut bytes.Buffer
	err = runStat(&out, &errOut, statConfig{opts: compare.DefaultOptions()}, []string{base.String(), newer.String()})
	if err != nil {
		t.Fatalf("runStat: %v\nstderr:\n%s", err, errOut.String())
	}
	if !strings.Contains(out.String(), "BenchmarkOld") {
		t.Fatalf("stat output omitted deleted historical module recording:\nstdout:\n%s\nstderr:\n%s", out.String(), errOut.String())
	}
}

func TestStatABDoesNotDiscoverSiblingModuleOutsideCurrentScope(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "modA", "go.mod"), "module example.com/modA\n\ngo 1.26.4\n")
	writeFile(t, filepath.Join(dir, "modB", "go.mod"), "module example.com/modB\n\ngo 1.26.4\n")

	repo, err := gogit.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("git init: %v", err)
	}
	st := store.New(filepath.Join(dir, "modB", "benchmarks"))
	writeStatRecording(t, st, "pkg", "BenchmarkSibling", 100)
	base := commitAll(t, repo, "base")
	writeStatRecording(t, st, "pkg", "BenchmarkSibling", 120)
	newer := commitAll(t, repo, "newer")

	withWorkingDir(t, filepath.Join(dir, "modA"))
	var out, errOut bytes.Buffer
	err = runStat(&out, &errOut, statConfig{opts: compare.DefaultOptions()}, []string{base.String(), newer.String()})
	if err != nil {
		t.Fatalf("runStat: %v\nstderr:\n%s", err, errOut.String())
	}
	if strings.Contains(out.String(), "BenchmarkSibling") {
		t.Fatalf("stat output included sibling module outside current scope:\nstdout:\n%s\nstderr:\n%s", out.String(), errOut.String())
	}
}

func TestStatWorkingTreeStalenessHonorsDirective(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/statdirective\n\ngo 1.26.4\n")
	writeFile(t, filepath.Join(dir, "bench_test.go"), "package statdirective\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\n//gofresh:pure\nfunc BenchmarkPureRead(b *testing.B) { _, _ = os.ReadFile(\"fixture.txt\") }\n")
	writeFile(t, filepath.Join(dir, "fixture.txt"), "fixture\n")
	repo, err := gogit.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	withWorkingDir(t, dir)
	e, _, err := newEngineAt(dir, dir, false, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	subject := gofresh.Subject{Package: "example.com/statdirective", Symbol: "BenchmarkPureRead"}
	fp, err := e.CaptureFor(t.Context(), subject, dir, gofresh.Measurement)
	if err != nil {
		t.Fatal(err)
	}
	observation, err := runtimeinput.Incomplete(dir, "package-test-binary:example.com/statdirective", "testlog lacks operation outcome evidence")
	if err != nil {
		t.Fatal(err)
	}
	st := store.New(filepath.Join(dir, "benchmarks"))
	cfg := append(runpkg.ProvenanceConfig("c1", false, fp.Guards, runpkg.Conditions{}), runpkg.ClosureConfig(fp.MaximalClosure))
	cfg = append(cfg, runpkg.RuntimeConfig(observation.Digest, observation.Manifest)...)
	cfg = append(cfg, runpkg.GofreshPurityConfig(fp.PurityAssertion))
	recs := []*benchfmt.Result{{Name: benchfmt.Name("PureRead"), Iters: 1, Values: []benchfmt.Value{{Value: 1, Unit: "sec/op"}}, Config: cfg}}
	if err := st.Write("", "BenchmarkPureRead", "", recs); err != nil {
		t.Fatal(err)
	}
	commitAll(t, repo, "recording")

	var out, errOut bytes.Buffer
	if err := runStat(&out, &errOut, statConfig{opts: compare.DefaultOptions()}, nil); err != nil {
		t.Fatalf("runStat valid: %v\nstderr:\n%s", err, errOut.String())
	}
	if strings.Contains(errOut.String(), "is unverifiable") || strings.Contains(errOut.String(), "is stale") {
		t.Fatalf("directive-backed current recording warned:\n%s", errOut.String())
	}
	recordingPath, err := st.Path("", "BenchmarkPureRead", "")
	if err != nil {
		t.Fatal(err)
	}
	recording, err := os.ReadFile(recordingPath)
	if err != nil {
		t.Fatal(err)
	}
	unversioned := bytes.Replace(recording, []byte("pew-format: 1\n"), nil, 1)
	if bytes.Equal(unversioned, recording) {
		t.Fatal("recording format line not found")
	}
	if err := os.WriteFile(recordingPath, unversioned, 0o644); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	errOut.Reset()
	if err := runStat(&out, &errOut, statConfig{opts: compare.DefaultOptions()}, nil); err != nil {
		t.Fatalf("runStat unversioned: %v", err)
	}
	if !strings.Contains(errOut.String(), "stale (format)") {
		t.Fatalf("unversioned working-tree recording did not warn:\n%s", errOut.String())
	}
	if err := os.WriteFile(recordingPath, recording, 0o644); err != nil {
		t.Fatal(err)
	}

	writeFile(t, filepath.Join(dir, "bench_test.go"), "package statdirective\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\n//gofresh:pure\nfunc BenchmarkPureRead(b *testing.B) { _, _ = os.ReadFile(\"fixture.txt\"); b.ReportAllocs() }\n")
	out.Reset()
	errOut.Reset()
	if err := runStat(&out, &errOut, statConfig{opts: compare.DefaultOptions()}, nil); err != nil {
		t.Fatalf("runStat stale: %v\nstderr:\n%s", err, errOut.String())
	}
	if !strings.Contains(errOut.String(), "is stale (closure)") {
		t.Fatalf("stale working-tree recording did not warn:\n%s", errOut.String())
	}
}

func TestStatSideCacheServesHistoricalParse(t *testing.T) {
	key := statSideKey{ref: "HEAD", pkgRel: "p", bench: "BenchmarkX"}
	want := []*benchfmt.Result{{Name: benchfmt.Name("X")}}
	m := &statModule{sides: map[statSideKey]statSide{key: {recs: want, ok: true}}}
	got, ok, err := m.readSide(key.ref, key.pkgRel, key.bench, key.label)
	if err != nil || !ok || len(got) != 1 || string(got[0].Name) != "X" {
		t.Fatalf("cached historical side = %v, %v, %v", got, ok, err)
	}
}

func TestDedupeStatModulesMergesSharedBenchDir(t *testing.T) {
	shared := filepath.Join(t.TempDir(), "benchmarks")
	key := statKey{pkgRel: "pkg", bench: "BenchmarkShared"}
	mods := []*statModule{
		{moduleDir: "/repo/modA", benchDir: shared, current: map[statKey]currentBench{}},
		{moduleDir: "/repo/modB", benchDir: shared, current: map[statKey]currentBench{key: {importPath: "example.com/modB/pkg", moduleDir: "/repo/modB"}}},
	}
	got := dedupeStatModules(mods)
	if len(got) != 1 {
		t.Fatalf("dedupeStatModules returned %d modules, want 1", len(got))
	}
	if got[0].current[key].importPath != "example.com/modB/pkg" {
		t.Fatalf("dedupeStatModules did not merge current benchmark metadata: %#v", got[0].current)
	}
}

func TestStatABRejectsFormatValidNonPewOppositeSide(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/statghost\n\ngo 1.26.4\n")
	writeFile(t, filepath.Join(dir, "pkg", "pkg.go"), "package pkg\n")

	repo, err := gogit.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("git init: %v", err)
	}
	st := store.New(filepath.Join(dir, "benchmarks"))
	ghostPath, err := st.Path("pkg", "BenchmarkGhost", "")
	if err != nil {
		t.Fatal(err)
	}
	writeStatRecording(t, st, "pkg", "BenchmarkGhost", 100)
	base := commitAll(t, repo, "base")
	writeFile(t, ghostPath, "machine: m1\ntoolchain: go-test\nbuildconfig: b1\nBenchmarkGhost-8 1 120 sec/op\n")
	newer := commitAll(t, repo, "newer")
	reader, err := gitblob.Open(dir)
	if err != nil {
		t.Fatalf("gitblob open: %v", err)
	}
	m := &statModule{benchDir: st.Root, store: st, repo: reader, keys: map[statKey]bool{}}
	if err := addRefInventory(m, base.String(), ""); err != nil {
		t.Fatalf("addRefInventory: %v", err)
	}
	if len(m.keys) != 1 {
		t.Fatalf("valid base recording missing from inventory: %v", sortedStatKeys(m.keys))
	}

	withWorkingDir(t, dir)
	var out, errOut bytes.Buffer
	err = runStat(&out, &errOut, statConfig{benchDir: st.Root, opts: compare.DefaultOptions()}, []string{base.String(), newer.String()})
	if err != nil {
		t.Fatalf("runStat: %v\nstderr:\n%s", err, errOut.String())
	}
	if strings.Contains(out.String(), "BenchmarkGhost") {
		t.Fatalf("stat output compared non-pew recording:\nstdout:\n%s\nstderr:\n%s", out.String(), errOut.String())
	}
	if !strings.Contains(errOut.String(), "stale (format)") {
		t.Fatalf("format-valid non-pew side was not rejected:\n%s", errOut.String())
	}
}

func TestReadSideRetainsStaleFormatBlobForReporting(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/statghostside\n\ngo 1.26.4\n")
	writeFile(t, filepath.Join(dir, "pkg", "pkg.go"), "package pkg\n")

	repo, err := gogit.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("git init: %v", err)
	}
	st := store.New(filepath.Join(dir, "benchmarks"))
	writeStatRecording(t, st, "pkg", "BenchmarkGhost", 100)
	base := commitAll(t, repo, "base")
	ghostPath, err := st.Path("pkg", "BenchmarkGhost", "")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, ghostPath, "machine: m1\ntoolchain: go-test\nbuildconfig: b1\nBenchmarkGhost-8 1 120 sec/op\n")
	newer := commitAll(t, repo, "newer")
	reader, err := gitblob.Open(dir)
	if err != nil {
		t.Fatalf("gitblob open: %v", err)
	}
	if _, ok, err := readSide(st, reader, base.String(), "pkg", "BenchmarkGhost", ""); err != nil || !ok {
		t.Fatalf("valid base side ok=%v err=%v", ok, err)
	}
	if recs, ok, err := readSide(st, reader, newer.String(), "pkg", "BenchmarkGhost", ""); err != nil || !ok || len(recs) == 0 {
		t.Fatalf("stale-format side unavailable for reporting: ok=%v len=%d err=%v", ok, len(recs), err)
	} else if _, _, formatOK := fingerprintFromConfig(recs[0].Config); formatOK {
		t.Fatal("stale-format historical side accepted")
	}
}

func TestAddRefInventoryReportsMalformedHistoricalRecording(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/statbad\n\ngo 1.26.4\n")
	writeFile(t, filepath.Join(dir, "pkg", "pkg.go"), "package pkg\n")

	repo, err := gogit.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("git init: %v", err)
	}
	st := store.New(filepath.Join(dir, "benchmarks"))
	badPath, err := st.Path("pkg", "BenchmarkBad", "")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, badPath, strings.Join([]string{
		"pew-format: 1",
		"commit: c1",
		"toolchain: go-test",
		"machine: m1",
		"buildconfig: b1",
		"runtimeconfig: r1",
		"dirty: false",
		"pew-closure: cl1",
		"pew-runtime: rt1",
		"pew-runtime-inputs: manifest1",
		"BenchmarkBad-8 nope 100 sec/op",
		"",
	}, "\n"))
	ref := commitAll(t, repo, "bad")
	reader, err := gitblob.Open(dir)
	if err != nil {
		t.Fatalf("gitblob open: %v", err)
	}
	m := &statModule{benchDir: st.Root, store: st, repo: reader, keys: map[statKey]bool{}}
	err = addRefInventory(m, ref.String(), "")
	if err == nil || !strings.Contains(err.Error(), "corrupt recording") {
		t.Fatalf("addRefInventory err=%v, want corrupt recording", err)
	}
}

// stripRecordingKey rewrites a stored recording without one config line,
// simulating a recording written before that field became mandatory.
func stripRecordingKey(t *testing.T, path, key string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, key+":") {
			out = append(out, line)
		}
	}
	writeFile(t, path, strings.Join(out, "\n"))
}

// TestStatSurfacesOldShapeRecordingsAutoMode pins spec §10.1's visibility
// contract at the exact moment a format change lands: the working tree and
// HEAD both hold recordings predating a mandatory field, so every candidate
// fails the shape check. The old-shape recordings must be inventoried — the
// empty comparison names the stale-format tally and the per-side warning
// fires — never reported as "no recordings on either side".
func TestStatSurfacesOldShapeRecordingsAutoMode(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/statoldshape\n\ngo 1.26.4\n")
	writeFile(t, filepath.Join(dir, "pkg", "pkg.go"), "package pkg\n")

	repo, err := gogit.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("git init: %v", err)
	}
	// HEAD carries no recording at all: the old-shape recording exists only
	// in the working tree, so its visibility rests entirely on the
	// working-tree inventory branch.
	commitAll(t, repo, "no-recordings")
	st := store.New(filepath.Join(dir, "benchmarks"))
	writeStatRecording(t, st, "pkg", "BenchmarkOld", 100)
	oldPath, err := st.Path("pkg", "BenchmarkOld", "")
	if err != nil {
		t.Fatal(err)
	}
	stripRecordingKey(t, oldPath, "pew-runconditions")

	withWorkingDir(t, dir)
	var out, errOut bytes.Buffer
	sc := statConfig{benchDir: st.Root, opts: compare.DefaultOptions()}
	if err := runStat(&out, &errOut, sc, nil); err != nil {
		t.Fatalf("runStat: %v\nstderr:\n%s", err, errOut.String())
	}
	if strings.Contains(out.String(), "no recordings on either side") {
		t.Fatalf("old-shape recordings reported as absent:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "stale format: 1") {
		t.Fatalf("empty comparison does not name the stale-format cause:\n%s", out.String())
	}
	if !strings.Contains(errOut.String(), "stale (format)") {
		t.Fatalf("no per-side stale-format warning:\n%s", errOut.String())
	}
}

// TestStatSurfacesOldShapeRecordingsABMode is the same visibility contract in
// A/B mode: both refs hold only old-shape recordings.
func TestStatSurfacesOldShapeRecordingsABMode(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/statoldab\n\ngo 1.26.4\n")
	writeFile(t, filepath.Join(dir, "pkg", "pkg.go"), "package pkg\n")

	repo, err := gogit.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("git init: %v", err)
	}
	st := store.New(filepath.Join(dir, "benchmarks"))
	writeStatRecording(t, st, "pkg", "BenchmarkOld", 100)
	oldPath, err := st.Path("pkg", "BenchmarkOld", "")
	if err != nil {
		t.Fatal(err)
	}
	stripRecordingKey(t, oldPath, "pew-runconditions")
	refA := commitAll(t, repo, "a")
	writeFile(t, filepath.Join(dir, "pkg", "pkg.go"), "package pkg\n\n// changed\n")
	refB := commitAll(t, repo, "b")

	withWorkingDir(t, dir)
	var out, errOut bytes.Buffer
	sc := statConfig{benchDir: st.Root, opts: compare.DefaultOptions()}
	if err := runStat(&out, &errOut, sc, []string{refA.String(), refB.String()}); err != nil {
		t.Fatalf("runStat: %v\nstderr:\n%s", err, errOut.String())
	}
	if strings.Contains(out.String(), "no recordings on either side") {
		t.Fatalf("old-shape recordings reported as absent:\n%s", out.String())
	}
	// Both sides' files are stale: the tally counts recording files, and each
	// side warns — neither file goes unmentioned (spec §10.1 per-side).
	if !strings.Contains(out.String(), "stale format: 2") {
		t.Fatalf("empty comparison does not count both stale files:\n%s", out.String())
	}
	if !strings.Contains(errOut.String(), refA.String()) || !strings.Contains(errOut.String(), refB.String()) {
		t.Fatalf("per-side warnings do not name both refs:\n%s", errOut.String())
	}
}

// TestStatCountsBothDirtySides mirrors the stale-format per-side contract for
// dirty baselines: an A/B where both refs resolve to dirty recordings warns
// each side and tallies each file — "dirty recording: 2", neither side silent.
func TestStatCountsBothDirtySides(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/statdirtyab\n\ngo 1.26.4\n")
	writeFile(t, filepath.Join(dir, "pkg", "pkg.go"), "package pkg\n")

	repo, err := gogit.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("git init: %v", err)
	}
	st := store.New(filepath.Join(dir, "benchmarks"))
	writeStatRecording(t, st, "pkg", "BenchmarkDirty", 100)
	dirtyPath, err := st.Path("pkg", "BenchmarkDirty", "")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(dirtyPath)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, dirtyPath, strings.Replace(string(data), "dirty: false", "dirty: true", 1))
	refA := commitAll(t, repo, "a")
	writeFile(t, filepath.Join(dir, "pkg", "pkg.go"), "package pkg\n\n// changed\n")
	refB := commitAll(t, repo, "b")

	withWorkingDir(t, dir)
	var out, errOut bytes.Buffer
	sc := statConfig{benchDir: st.Root, opts: compare.DefaultOptions()}
	if err := runStat(&out, &errOut, sc, []string{refA.String(), refB.String()}); err != nil {
		t.Fatalf("runStat: %v\nstderr:\n%s", err, errOut.String())
	}
	if !strings.Contains(out.String(), "dirty recording: 2") {
		t.Fatalf("empty comparison does not count both dirty files:\n%s", out.String())
	}
	if !strings.Contains(errOut.String(), "baseline "+refA.String()) || !strings.Contains(errOut.String(), "new side "+refB.String()) {
		t.Fatalf("per-side dirty warnings incomplete:\n%s", errOut.String())
	}
}

// TestStatIgnoresForeignLayoutFiles pins the foreign-file boundary of the
// §10.1 inventory: a benchmark file with no pew-owned key at a layout path
// never inventories a comparison key, so an otherwise-empty store still
// reports "no recordings on either side" with no stale-format warning.
func TestStatIgnoresForeignLayoutFiles(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/statforeign\n\ngo 1.26.4\n")
	writeFile(t, filepath.Join(dir, "pkg", "pkg.go"), "package pkg\n")

	repo, err := gogit.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("git init: %v", err)
	}
	st := store.New(filepath.Join(dir, "benchmarks"))
	foreignPath, err := st.Path("pkg", "BenchmarkForeign", "")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, foreignPath, "goos: linux\nBenchmarkForeign-8 1 100 sec/op\n")
	commitAll(t, repo, "foreign")

	withWorkingDir(t, dir)
	var out, errOut bytes.Buffer
	sc := statConfig{benchDir: st.Root, opts: compare.DefaultOptions()}
	if err := runStat(&out, &errOut, sc, nil); err != nil {
		t.Fatalf("runStat: %v\nstderr:\n%s", err, errOut.String())
	}
	if !strings.Contains(out.String(), "no recordings on either side") {
		t.Fatalf("foreign file inventoried a key:\n%s", out.String())
	}
	if strings.Contains(errOut.String(), "stale (format)") {
		t.Fatalf("foreign file mislabeled stale (format):\n%s", errOut.String())
	}
}

func withWorkingDir(t *testing.T, dir string) {
	t.Helper()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeStatRecording(t *testing.T, st *store.Store, pkgRel, bench string, value float64) {
	t.Helper()
	writeStatRecordingConditions(t, st, pkgRel, bench, value, "governor=performance turbo=off load1=0.03 throttled=false battery=false")
}

func writeStatRecordingConditions(t *testing.T, st *store.Store, pkgRel, bench string, value float64, conditions string) {
	t.Helper()
	var recs []*benchfmt.Result
	for i := range 8 {
		recs = append(recs, &benchfmt.Result{
			Name:  benchfmt.Name(bench),
			Iters: 1,
			Values: []benchfmt.Value{
				{Value: value + float64(i), Unit: "sec/op"},
			},
			Config: []benchfmt.Config{
				{Key: "pew-format", Value: []byte(runpkg.RecordingFormat), File: true},
				{Key: "commit", Value: []byte("c1"), File: true},
				{Key: "toolchain", Value: []byte("go-test"), File: true},
				{Key: "machine", Value: []byte("m1"), File: true},
				{Key: "buildconfig", Value: []byte("b1"), File: true},
				{Key: "runtimeconfig", Value: []byte("r1"), File: true},
				{Key: "dirty", Value: []byte("false"), File: true},
				{Key: "pew-runconditions", Value: []byte(conditions), File: true},
				{Key: "pew-closure", Value: []byte("cl1"), File: true},
				{Key: "pew-runtime", Value: []byte("rt1"), File: true},
				{Key: "pew-runtime-inputs", Value: []byte("manifest1"), File: true},
			},
		})
	}
	if err := st.Write(pkgRel, bench, "", recs); err != nil {
		t.Fatalf("Write(%q,%q): %v", pkgRel, bench, err)
	}
}

func commitAll(t *testing.T, repo *gogit.Repository, msg string) plumbing.Hash {
	t.Helper()
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatalf("worktree: %v", err)
	}
	if err := wt.AddGlob("."); err != nil {
		t.Fatalf("git add: %v", err)
	}
	h, err := wt.Commit(msg, &gogit.CommitOptions{Author: &object.Signature{Name: "pew test", Email: "pew@example.invalid", When: time.Unix(1, 0)}})
	if err != nil {
		t.Fatalf("git commit: %v", err)
	}
	return h
}

func TestIsDirty(t *testing.T) {
	dirty := []*benchfmt.Result{{Config: []benchfmt.Config{{Key: "dirty", Value: []byte("true"), File: true}}}}
	clean := []*benchfmt.Result{{Config: []benchfmt.Config{{Key: "dirty", Value: []byte("false"), File: true}}}}
	none := []*benchfmt.Result{{}}
	if !isDirty(dirty) {
		t.Error("isDirty(dirty)=false")
	}
	if isDirty(clean) {
		t.Error("isDirty(clean)=true")
	}
	if isDirty(none) {
		t.Error("isDirty(no-flag)=true")
	}
	if isDirty(nil) {
		t.Error("isDirty(nil)=true")
	}
}

func TestNonValidUsesLabel(t *testing.T) {
	e, err := gofresh.New()
	if err != nil {
		t.Fatalf("New engine: %v", err)
	}
	const pkg = "github.com/greatliontech/pew/internal/fixtures/bench"
	const bench = "BenchmarkDecode"
	fp, err := e.CaptureFor(t.Context(), gofresh.Subject{Package: pkg, Symbol: bench}, ".", gofresh.Measurement)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	rt, err := runtimeinput.Incomplete(".", "package-test-binary:non-valid", "testlog lacks operation outcome evidence")
	if err != nil {
		t.Fatalf("runtime inputs: %v", err)
	}
	st := store.New(t.TempDir())
	write := func(label, hash string) {
		t.Helper()
		// The recorded guards must be the values the engine recomputes at check
		// time, so the closure hash alone decides the verdict.
		cfg := []benchfmt.Config{
			{Key: "pew-format", Value: []byte(runpkg.RecordingFormat), File: true},
			{Key: "commit", Value: []byte("c1"), File: true},
			{Key: "toolchain", Value: []byte(fp.Guards.Toolchain), File: true},
			{Key: "machine", Value: []byte(fp.Guards.Machine), File: true},
			{Key: "buildconfig", Value: []byte(fp.Guards.BuildConfig), File: true},
			{Key: "runtimeconfig", Value: []byte(fp.Guards.RuntimeConfig), File: true},
			{Key: "pew-runconditions", Value: []byte("governor=performance turbo=off load1=0.03 throttled=false battery=false"), File: true},
			{Key: "pew-closure", Value: []byte(hash), File: true},
			{Key: "pew-runtime", Value: []byte(rt.Digest), File: true},
			{Key: "pew-runtime-inputs", Value: []byte(rt.Manifest), File: true},
			{Key: "pew-purity", Value: []byte(fp.PurityAssertion), File: true},
			{Key: "dirty", Value: []byte("false"), File: true},
		}
		recs := []*benchfmt.Result{{Name: benchfmt.Name(bench), Iters: 1, Values: []benchfmt.Value{{Value: 1, Unit: "sec/op"}}, Config: cfg}}
		if err := st.Write("", bench, label, recs); err != nil {
			t.Fatalf("Write(%q): %v", label, err)
		}
	}
	write("", fp.MaximalClosure)
	write("x", fp.MaximalClosure+"-stale")

	need, err := nonValid(st, e, pkg, "", ".", "x", []string{bench})
	if err != nil {
		t.Fatalf("nonValid labeled: %v", err)
	}
	if len(need) != 1 || need[0] != bench {
		t.Fatalf("labeled nonValid = %v, want [%s]", need, bench)
	}
	need, err = nonValid(st, e, pkg, "", ".", "", []string{bench})
	if err != nil {
		t.Fatalf("nonValid unlabeled: %v", err)
	}
	if len(need) != 0 {
		t.Fatalf("unlabeled nonValid = %v, want none", need)
	}
}

// TestRunConditionsDoNotAffectValidity is INV-9's validity anchor: two
// recordings identical except for their recorded run conditions get the same
// verdict — run conditions are provenance, never a staleness guard (§8, §9).
func TestRunConditionsDoNotAffectValidity(t *testing.T) {
	e, err := gofresh.New()
	if err != nil {
		t.Fatalf("New engine: %v", err)
	}
	const pkg = "github.com/greatliontech/pew/internal/fixtures/bench"
	const bench = "BenchmarkDecode"
	fp, err := e.CaptureFor(t.Context(), gofresh.Subject{Package: pkg, Symbol: bench}, ".", gofresh.Measurement)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	rt, err := runtimeinput.Incomplete(".", "package-test-binary:run-conditions", "testlog lacks operation outcome evidence")
	if err != nil {
		t.Fatal(err)
	}
	st := store.New(t.TempDir())
	write := func(label, conditions string) {
		t.Helper()
		cfg := []benchfmt.Config{
			{Key: "pew-format", Value: []byte(runpkg.RecordingFormat), File: true},
			{Key: "commit", Value: []byte("c1"), File: true},
			{Key: "toolchain", Value: []byte(fp.Guards.Toolchain), File: true},
			{Key: "machine", Value: []byte(fp.Guards.Machine), File: true},
			{Key: "buildconfig", Value: []byte(fp.Guards.BuildConfig), File: true},
			{Key: "runtimeconfig", Value: []byte(fp.Guards.RuntimeConfig), File: true},
			{Key: "pew-runconditions", Value: []byte(conditions), File: true},
			{Key: "pew-closure", Value: []byte(fp.MaximalClosure), File: true},
			{Key: "pew-runtime", Value: []byte(rt.Digest), File: true},
			{Key: "pew-runtime-inputs", Value: []byte(rt.Manifest), File: true},
			{Key: "pew-purity", Value: []byte(fp.PurityAssertion), File: true},
			{Key: "dirty", Value: []byte("false"), File: true},
		}
		recs := []*benchfmt.Result{{Name: benchfmt.Name(bench), Iters: 1, Values: []benchfmt.Value{{Value: 1, Unit: "sec/op"}}, Config: cfg}}
		if err := st.Write("", bench, label, recs); err != nil {
			t.Fatalf("Write(%q): %v", label, err)
		}
	}
	write("quiet", "governor=performance turbo=off load1=0.03 throttled=false battery=false")
	write("noisy", "governor=powersave turbo=on load1=7.50 throttled=true battery=true")
	write("unknown", "governor=unknown turbo=unknown load1=unknown throttled=unknown battery=unknown")

	for _, label := range []string{"quiet", "noisy", "unknown"} {
		v, reason, _, err := checkOne(st, e, pkg, "", ".", bench, label)
		if err != nil {
			t.Fatalf("checkOne(%s): %v", label, err)
		}
		if v != verdictValid {
			t.Errorf("checkOne(%s) = {%s %q}, want valid — run conditions leaked into a staleness guard", label, v, reason)
		}
	}
}

// TestStatABNotesDifferingRunConditions drives the §10.1 surface end to end:
// two committed recordings differing in run conditions are compared (table
// printed) with a differing-conditions note.
func TestStatABNotesDifferingRunConditions(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/statconds\n\ngo 1.26.4\n")
	writeFile(t, filepath.Join(dir, "pkg", "pkg.go"), "package pkg\n")

	repo, err := gogit.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("git init: %v", err)
	}
	st := store.New(filepath.Join(dir, "benchmarks"))
	writeStatRecordingConditions(t, st, "pkg", "BenchmarkConds", 100, "governor=performance turbo=off load1=0.03 throttled=false battery=false")
	base := commitAll(t, repo, "base")
	writeStatRecordingConditions(t, st, "pkg", "BenchmarkConds", 120, "governor=powersave turbo=on load1=6.41 throttled=false battery=false")
	newer := commitAll(t, repo, "newer")

	withWorkingDir(t, dir)
	var out, errOut bytes.Buffer
	err = runStat(&out, &errOut, statConfig{benchDir: st.Root, opts: compare.DefaultOptions()}, []string{base.String(), newer.String()})
	if err != nil {
		t.Fatalf("runStat: %v\nstderr:\n%s", err, errOut.String())
	}
	if !strings.Contains(out.String(), "BenchmarkConds") || !strings.Contains(out.String(), "sec/op") {
		t.Fatalf("differing conditions blocked the comparison:\nstdout:\n%s\nstderr:\n%s", out.String(), errOut.String())
	}
	if !strings.Contains(out.String(), "run conditions differ") {
		t.Fatalf("stat output missing the differing-conditions note:\nstdout:\n%s\nstderr:\n%s", out.String(), errOut.String())
	}
}

// TestCheckOneAppliesMeasurementGuards pins that a benchmark verdict is checked
// under the measurement-guard policy: a recording whose machine guard differs from
// the current machine is stale, which only a Measurement-kind check catches (a
// code-result check ignores the machine guard).
func TestCheckOneAppliesMeasurementGuards(t *testing.T) {
	e, err := gofresh.New()
	if err != nil {
		t.Fatalf("New engine: %v", err)
	}
	const pkg = "github.com/greatliontech/pew/internal/fixtures/bench"
	const bench = "BenchmarkDecode"
	fp, err := e.CaptureFor(t.Context(), gofresh.Subject{Package: pkg, Symbol: bench}, ".", gofresh.Measurement)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	rt, err := runtimeinput.Incomplete(".", "measurement-test", "test observation incomplete")
	if err != nil {
		t.Fatal(err)
	}
	st := store.New(t.TempDir())
	cfg := []benchfmt.Config{
		{Key: "pew-format", Value: []byte(runpkg.RecordingFormat), File: true},
		{Key: "commit", Value: []byte("c1"), File: true},
		{Key: "toolchain", Value: []byte(fp.Guards.Toolchain), File: true},
		{Key: "machine", Value: []byte("some-other-machine"), File: true},
		{Key: "buildconfig", Value: []byte(fp.Guards.BuildConfig), File: true},
		{Key: "runtimeconfig", Value: []byte(fp.Guards.RuntimeConfig), File: true},
		{Key: "pew-runconditions", Value: []byte("governor=performance turbo=off load1=0.03 throttled=false battery=false"), File: true},
		{Key: "pew-closure", Value: []byte(fp.MaximalClosure), File: true},
		{Key: "pew-runtime", Value: []byte(rt.Digest), File: true},
		{Key: "pew-runtime-inputs", Value: []byte(rt.Manifest), File: true},
		{Key: "dirty", Value: []byte("false"), File: true},
	}
	recs := []*benchfmt.Result{{Name: benchfmt.Name(bench), Iters: 1, Values: []benchfmt.Value{{Value: 1, Unit: "sec/op"}}, Config: cfg}}
	if err := st.Write("", bench, "", recs); err != nil {
		t.Fatalf("Write: %v", err)
	}
	v, reason, _, err := checkOne(st, e, pkg, "", ".", bench, "")
	if err != nil {
		t.Fatalf("checkOne: %v", err)
	}
	if v != verdictStale || reason != "machine" {
		t.Errorf("checkOne = {%s %q}, want {stale machine}", v, reason)
	}

	// End to end through checkOne, a recording flagged --impure (pure: false) whose
	// guards all hold is unverifiable "impure": it always re-runs (§7.3).
	impCfg := []benchfmt.Config{
		{Key: "pew-format", Value: []byte(runpkg.RecordingFormat), File: true},
		{Key: "commit", Value: []byte("c1"), File: true},
		{Key: "toolchain", Value: []byte(fp.Guards.Toolchain), File: true},
		{Key: "machine", Value: []byte(fp.Guards.Machine), File: true},
		{Key: "buildconfig", Value: []byte(fp.Guards.BuildConfig), File: true},
		{Key: "runtimeconfig", Value: []byte(fp.Guards.RuntimeConfig), File: true},
		{Key: "pew-runconditions", Value: []byte("governor=performance turbo=off load1=0.03 throttled=false battery=false"), File: true},
		{Key: "pew-closure", Value: []byte(fp.MaximalClosure), File: true},
		{Key: "pew-runtime", Value: []byte(rt.Digest), File: true},
		{Key: "pew-runtime-inputs", Value: []byte(rt.Manifest), File: true},
		{Key: "dirty", Value: []byte("false"), File: true},
		{Key: "pure", Value: []byte("false"), File: true},
	}
	impRecs := []*benchfmt.Result{{Name: benchfmt.Name(bench), Iters: 1, Values: []benchfmt.Value{{Value: 1, Unit: "sec/op"}}, Config: impCfg}}
	if err := st.Write("", bench, "imp", impRecs); err != nil {
		t.Fatalf("Write imp: %v", err)
	}
	v, reason, _, err = checkOne(st, e, pkg, "", ".", bench, "imp")
	if err != nil {
		t.Fatalf("checkOne imp: %v", err)
	}
	if v != verdictUnverifiable || reason != "impure" {
		t.Errorf("checkOne imp = {%s %q}, want {unverifiable impure}", v, reason)
	}
}

// TestStatFailOnRegressionEmptyStoreFailsClosed pins the §10.1 empty-comparison
// gate: with nothing recorded on either side, --fail-on-regression must not
// exit clean (the gate would pass precisely when it measured nothing), while
// the flagless invocation stays informational and names why nothing compared.
func TestStatFailOnRegressionEmptyStoreFailsClosed(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/statempty\n\ngo 1.26.4\n")
	repo, err := gogit.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("git init: %v", err)
	}
	commitAll(t, repo, "base")
	withWorkingDir(t, dir)

	var out, errOut bytes.Buffer
	sc := statConfig{opts: compare.DefaultOptions(), failOnRegression: true}
	err = runStat(&out, &errOut, sc, nil)
	if err == nil {
		t.Fatalf("empty comparison under --fail-on-regression exited clean\nstdout:\n%s\nstderr:\n%s", out.String(), errOut.String())
	}
	var empty *nothingComparedError
	if !errors.As(err, &empty) {
		t.Fatalf("err = %v (%T), want *nothingComparedError", err, err)
	}
	if !strings.Contains(err.Error(), "no recordings on either side") {
		t.Fatalf("gate diagnostic does not name the cause: %v", err)
	}

	// Without the flag the same empty comparison is informational: no error
	// (exit 0) and the no-benchmarks line names why.
	out.Reset()
	errOut.Reset()
	sc.failOnRegression = false
	if err := runStat(&out, &errOut, sc, nil); err != nil {
		t.Fatalf("informational empty comparison errored: %v", err)
	}
	if !strings.Contains(out.String(), "no recorded benchmarks to compare: no recordings on either side") {
		t.Fatalf("informational empty message does not name why:\n%s", out.String())
	}
}

// TestStatFailOnRegressionAllSkippedFailsClosed: every inventoried candidate is
// skipped (the new side fails the recording shape), so zero benchmarks are
// statistically compared — the gate fails with the per-cause tally rather than
// passing vacuously (spec §10.1).
func TestStatFailOnRegressionAllSkippedFailsClosed(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/statskipgate\n\ngo 1.26.4\n")
	writeFile(t, filepath.Join(dir, "pkg", "pkg.go"), "package pkg\n")

	repo, err := gogit.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("git init: %v", err)
	}
	st := store.New(filepath.Join(dir, "benchmarks"))
	ghostPath, err := st.Path("pkg", "BenchmarkGhost", "")
	if err != nil {
		t.Fatal(err)
	}
	writeStatRecording(t, st, "pkg", "BenchmarkGhost", 100)
	base := commitAll(t, repo, "base")
	writeFile(t, ghostPath, "machine: m1\ntoolchain: go-test\nbuildconfig: b1\nBenchmarkGhost-8 1 120 sec/op\n")
	newer := commitAll(t, repo, "newer")

	withWorkingDir(t, dir)
	var out, errOut bytes.Buffer
	sc := statConfig{benchDir: st.Root, opts: compare.DefaultOptions(), failOnRegression: true}
	err = runStat(&out, &errOut, sc, []string{base.String(), newer.String()})
	var empty *nothingComparedError
	if !errors.As(err, &empty) {
		t.Fatalf("all-skipped comparison under --fail-on-regression: err = %v (%T), want *nothingComparedError\nstderr:\n%s", err, err, errOut.String())
	}
	if !strings.Contains(err.Error(), "stale format: 1") {
		t.Fatalf("gate diagnostic does not carry the skip tally: %v", err)
	}
}

// TestStatFailOnRegressionPartialSkipGovernedByComparedSubset: when some
// benchmarks compare and others are skipped, the compared subset alone governs
// the exit (spec §10.1) — clean compared rows pass the gate despite skips, and
// a regressing compared row fails it as a regression, not as empty.
func TestStatFailOnRegressionPartialSkipGovernedByComparedSubset(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/statpartial\n\ngo 1.26.4\n")
	writeFile(t, filepath.Join(dir, "pkg", "pkg.go"), "package pkg\n")

	repo, err := gogit.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("git init: %v", err)
	}
	st := store.New(filepath.Join(dir, "benchmarks"))
	ghostPath, err := st.Path("pkg", "BenchmarkGhost", "")
	if err != nil {
		t.Fatal(err)
	}
	writeStatRecording(t, st, "pkg", "BenchmarkGood", 100)
	writeStatRecording(t, st, "pkg", "BenchmarkGhost", 100)
	base := commitAll(t, repo, "base")
	writeStatRecording(t, st, "pkg", "BenchmarkGood", 100) // unchanged: no regression
	writeFile(t, ghostPath, "machine: m1\ntoolchain: go-test\nbuildconfig: b1\nBenchmarkGhost-8 1 120 sec/op\n")
	clean := commitAll(t, repo, "clean")
	writeStatRecording(t, st, "pkg", "BenchmarkGood", 130) // ~30% worse: regression
	regressed := commitAll(t, repo, "regressed")

	withWorkingDir(t, dir)
	sc := statConfig{benchDir: st.Root, opts: compare.DefaultOptions(), failOnRegression: true}

	var out, errOut bytes.Buffer
	if err := runStat(&out, &errOut, sc, []string{base.String(), clean.String()}); err != nil {
		t.Fatalf("clean compared subset did not govern the exit: %v\nstderr:\n%s", err, errOut.String())
	}
	if !strings.Contains(errOut.String(), "stale (format)") {
		t.Fatalf("skipped benchmark not surfaced:\n%s", errOut.String())
	}

	out.Reset()
	errOut.Reset()
	err = runStat(&out, &errOut, sc, []string{base.String(), regressed.String()})
	if err == nil || err.Error() != "regression detected" {
		t.Fatalf("regressing compared subset: err = %v, want regression detected", err)
	}
	var empty *nothingComparedError
	if errors.As(err, &empty) {
		t.Fatalf("regression misclassified as empty comparison: %v", err)
	}
}

// TestExitCode pins the exit-status contract (spec §10.1): every error exits 1
// — including "regression detected" — except the empty-comparison gate
// failure, which exits 2 so CI can tell "regressed" from "measured nothing".
func TestExitCode(t *testing.T) {
	if got := exitCode(errors.New("regression detected")); got != 1 {
		t.Errorf("exitCode(regression) = %d, want 1", got)
	}
	if got := exitCode(errors.New("any other failure")); got != 1 {
		t.Errorf("exitCode(generic) = %d, want 1", got)
	}
	if got := exitCode(&nothingComparedError{reason: "no recordings on either side"}); got != 2 {
		t.Errorf("exitCode(nothing compared) = %d, want 2", got)
	}
	if got := exitCode(fmt.Errorf("wrapped: %w", &nothingComparedError{reason: "x"})); got != 2 {
		t.Errorf("exitCode(wrapped nothing compared) = %d, want 2", got)
	}
}

// TestStatExplainShowsSideBySideGuards pins spec §12's stat --explain: an A/B
// whose sides disagree on a guard prints the recorded values side by side,
// naming the moving guard behind the one-word mismatch note.
func TestStatExplainShowsSideBySideGuards(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/statexplain\n\ngo 1.26.4\n")
	writeFile(t, filepath.Join(dir, "pkg", "pkg.go"), "package pkg\n")

	repo, err := gogit.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("git init: %v", err)
	}
	st := store.New(filepath.Join(dir, "benchmarks"))
	writeStatRecording(t, st, "pkg", "BenchmarkGuard", 100)
	refA := commitAll(t, repo, "a")
	// The new side's buildconfig moves: recorded values must land side by side.
	path, err := st.Path("pkg", "BenchmarkGuard", "")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, strings.Replace(string(data), "buildconfig: b1", "buildconfig: b2", 1))
	refB := commitAll(t, repo, "b")

	withWorkingDir(t, dir)
	var out, errOut bytes.Buffer
	sc := statConfig{benchDir: st.Root, opts: compare.DefaultOptions(), explain: true}
	if err := runStat(&out, &errOut, sc, []string{refA.String(), refB.String()}); err != nil {
		t.Fatalf("runStat: %v\nstderr:\n%s", err, errOut.String())
	}
	got := errOut.String()
	if !strings.Contains(got, "guard mismatch between") {
		t.Fatalf("no explanation header:\n%s", got)
	}
	if !strings.Contains(got, "buildconfig") || !strings.Contains(got, "b1") || !strings.Contains(got, "b2") || !strings.Contains(got, "NO") {
		t.Fatalf("side-by-side table does not name the moving guard:\n%s", got)
	}
}

// TestStatExplainWorkingTreeStaleness pins §12's second stat --explain arm: a
// working-tree recording warned non-valid prints the recorded-vs-current
// explanation below the warning.
func TestStatExplainWorkingTreeStaleness(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/statwtexplain\n\ngo 1.26.4\n")
	writeFile(t, filepath.Join(dir, "pkg", "pkg_test.go"), "package pkg\n\nimport \"testing\"\n\nfunc BenchmarkWT(b *testing.B) {}\n")

	repo, err := gogit.PlainInit(dir, false)
	if err != nil {
		t.Fatalf("git init: %v", err)
	}
	st := store.New(filepath.Join(dir, "benchmarks"))
	// The recorded toolchain "go-test" cannot be current: the staleness
	// warning fires and the explanation must lay the values side by side.
	writeStatRecording(t, st, "pkg", "BenchmarkWT", 100)
	commitAll(t, repo, "base")

	withWorkingDir(t, dir)
	var out, errOut bytes.Buffer
	sc := statConfig{benchDir: st.Root, opts: compare.DefaultOptions(), explain: true}
	if err := runStat(&out, &errOut, sc, nil); err != nil {
		t.Fatalf("runStat: %v\nstderr:\n%s", err, errOut.String())
	}
	got := errOut.String()
	if !strings.Contains(got, "comparison may not reflect HEAD") {
		t.Fatalf("no working-tree staleness warning:\n%s", got)
	}
	if !strings.Contains(got, "recorded") || !strings.Contains(got, "current") || !strings.Contains(got, "toolchain") || !strings.Contains(got, "NO") {
		t.Fatalf("no recorded-vs-current explanation under the warning:\n%s", got)
	}
}
