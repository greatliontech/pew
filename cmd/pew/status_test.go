package main

import (
	"strings"
	"testing"

	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/runtimeinput"
	runpkg "github.com/greatliontech/pew/internal/run"
	"github.com/greatliontech/pew/internal/store"
	"golang.org/x/perf/benchfmt"
)

// TestStatusPackageUsesLabel pins the status --label semantics (spec §12, §6):
// the verdict is checked against the labeled recording, so a benchmark recorded
// only under a label is unrecorded without --label and visible with it.
func TestStatusPackageUsesLabel(t *testing.T) {
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
	rt, err := runtimeinput.Incomplete(".", "package-test-binary:status-label", "testlog lacks operation outcome evidence")
	if err != nil {
		t.Fatalf("runtime inputs: %v", err)
	}
	st := store.New(t.TempDir())
	// The recorded guards are the values the engine recomputes at check time,
	// so only the label decides whether a recording is found.
	cfg := []benchfmt.Config{
		{Key: "pew-format", Value: []byte(runpkg.RecordingFormat), File: true},
		{Key: "commit", Value: []byte("c1"), File: true},
		{Key: "toolchain", Value: []byte(fp.Guards.Toolchain), File: true},
		{Key: "machine", Value: []byte(fp.Guards.Machine), File: true},
		{Key: "buildconfig", Value: []byte(fp.Guards.BuildConfig), File: true},
		{Key: "runtimeconfig", Value: []byte(fp.Guards.RuntimeConfig), File: true},
		{Key: "pew-closure", Value: []byte(fp.MaximalClosure), File: true},
		{Key: "pew-runtime", Value: []byte(rt.Digest), File: true},
		{Key: "pew-runtime-inputs", Value: []byte(rt.Manifest), File: true},
		{Key: "pew-purity", Value: []byte(fp.PurityAssertion), File: true},
		{Key: "dirty", Value: []byte("false"), File: true},
		{Key: "pew-runconditions", Value: []byte("governor=performance turbo=off load1=0.03 throttled=false battery=false"), File: true},
	}
	recs := []*benchfmt.Result{{Name: benchfmt.Name(bench), Iters: 1, Values: []benchfmt.Value{{Value: 1, Unit: "sec/op"}}, Config: cfg}}
	if err := st.Write("internal/fixtures/bench", bench, "x", recs); err != nil {
		t.Fatalf("Write: %v", err)
	}

	p := pkgMeta{ImportPath: pkg, Dir: "../../internal/fixtures/bench", TestGoFiles: []string{"bench_test.go"}}
	p.Module.Path = "github.com/greatliontech/pew"
	p.Module.Dir = "."

	var out strings.Builder
	if err := statusPackage(&out, e, st.Root, "x", false, p); err != nil {
		t.Fatalf("statusPackage labeled: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "valid") || !strings.Contains(got, bench) {
		t.Errorf("labeled status = %q, want a valid row for %s", got, bench)
	}

	out.Reset()
	if err := statusPackage(&out, e, st.Root, "", false, p); err != nil {
		t.Fatalf("statusPackage unlabeled: %v", err)
	}
	if got := out.String(); !strings.Contains(got, "unrecorded") {
		t.Errorf("unlabeled status = %q, want unrecorded", got)
	}
}
