package main

import (
	"bytes"
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
	e, err := newEngine(dir)
	if err != nil {
		t.Fatal(err)
	}
	subject := gofresh.Subject{Package: "example.com/statdirective", Symbol: "BenchmarkPureRead"}
	fp, err := e.CaptureFor(subject, dir, gofresh.Measurement)
	if err != nil {
		t.Fatal(err)
	}
	observation, err := runtimeinput.Incomplete(dir, "package-test-binary:example.com/statdirective", "testlog lacks operation outcome evidence")
	if err != nil {
		t.Fatal(err)
	}
	st := store.New(filepath.Join(dir, "benchmarks"))
	cfg := append(runpkg.ProvenanceConfig("c1", false, fp.Guards), runpkg.ClosureConfig(fp.MaximalClosure))
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
	fp, err := e.CaptureFor(gofresh.Subject{Package: pkg, Symbol: bench}, ".", gofresh.Measurement)
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
	fp, err := e.CaptureFor(gofresh.Subject{Package: pkg, Symbol: bench}, ".", gofresh.Measurement)
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
		{Key: "pew-closure", Value: []byte(fp.MaximalClosure), File: true},
		{Key: "pew-runtime", Value: []byte(rt.Digest), File: true},
		{Key: "pew-runtime-inputs", Value: []byte(rt.Manifest), File: true},
		{Key: "dirty", Value: []byte("false"), File: true},
	}
	recs := []*benchfmt.Result{{Name: benchfmt.Name(bench), Iters: 1, Values: []benchfmt.Value{{Value: 1, Unit: "sec/op"}}, Config: cfg}}
	if err := st.Write("", bench, "", recs); err != nil {
		t.Fatalf("Write: %v", err)
	}
	v, reason, err := checkOne(st, e, pkg, "", ".", bench, "")
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
	v, reason, err = checkOne(st, e, pkg, "", ".", bench, "imp")
	if err != nil {
		t.Fatalf("checkOne imp: %v", err)
	}
	if v != verdictUnverifiable || reason != "impure" {
		t.Errorf("checkOne imp = {%s %q}, want {unverifiable impure}", v, reason)
	}
}
