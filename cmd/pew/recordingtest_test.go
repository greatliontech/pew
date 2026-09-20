package main

import (
	"reflect"
	"testing"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/guard"

	"github.com/greatliontech/pew/internal/recordingtest"
	runpkg "github.com/greatliontech/pew/internal/run"
	"github.com/greatliontech/pew/internal/store"
	"golang.org/x/perf/benchfmt"
)

// TestRecordingBuilderIsTheWritersShape: the one test recording builder
// composes exactly what `pew run` writes — its default recording is a
// current-shape recording every reader admits, its fingerprint restores
// through the reader the writer is paired with, and its row edits are
// real: an omitted mandatory row fails the shape check, a set row
// replaces the writer's line rather than duplicating it.
func TestRecordingBuilderIsTheWritersShape(t *testing.T) {
	recs := recordingtest.Results("BenchmarkX", []float64{1, 2})
	if !store.IsRecording(recs) || !store.IsRecordingShape(recs) {
		t.Fatalf("the default recording is not a current-shape recording: %+v", recs[0].Config)
	}
	fp, ledger, ok := fingerprintFromConfig(recs[0].Config)
	if !ok || fp.MaximalClosure != "cl1" || ledger != "ledger1" || fp.Guards.Toolchain != "go-test" {
		t.Fatalf("the reader did not restore the builder's defaults: ok=%v fp=%+v ledger=%q", ok, fp, ledger)
	}
	omitted := recordingtest.Results("BenchmarkX", []float64{1}, recordingtest.Omit(runpkg.KeyCommit))
	if store.IsRecordingShape(omitted) {
		t.Fatal("an omitted mandatory row still passes the shape check")
	}
	for _, c := range omitted[0].Config {
		if c.Key == runpkg.KeyCommit.Name {
			t.Fatalf("Omit left the row in place (value %q); an omitted row is absent, never empty", c.Value)
		}
	}
	// The options are the writer's inputs, not row edits over a fixed
	// shape: the conditions line and a measured fingerprint reach the
	// lines through the writer's own composition.
	noisy := "governor=powersave turbo=on load1=6.41 throttled=false battery=false"
	if v := configValue(recordingtest.Config(recordingtest.Conditions(noisy)), runpkg.KeyRunConditions); v != noisy {
		t.Fatalf("Conditions did not reach the run-conditions row: %q", v)
	}
	want := gofresh.Fingerprint{
		MaximalClosure: "closure-x", ClosureStrategy: "strategy-x", TestVariantClosure: "variant-x",
		Guards:          guard.Guards{Toolchain: "tc-x", Machine: "m-x", BuildConfig: "b-x", RuntimeConfig: "r-x"},
		PurityAssertion: "purity-x", DynamicStateVouches: "v-x", SingleSubjectDischarges: "s-x", PackageProcessDischarges: "p-x",
		DynamicStateStrategy: "dyn-x", RuntimeInputs: "manifest-x", RuntimeDigest: "digest-x", ResultKind: gofresh.Measurement,
	}
	got, gotLedger, ok := fingerprintFromConfig(recordingtest.Config(recordingtest.Measured(want, "ledger-x", want.RuntimeDigest, want.RuntimeInputs)))
	if !ok || !reflect.DeepEqual(got, want) || gotLedger != "ledger-x" {
		t.Fatalf("Measured did not reach the lines: ok=%v\n got %+v\nwant %+v (ledger %q)", ok, got, want, gotLedger)
	}
	for _, c := range recs[0].Config {
		if !runpkg.IsRecordingKey(c.Key) {
			t.Fatalf("the builder composed a row outside the closed set: %s", c.Key)
		}
	}
	set := recordingtest.Config(recordingtest.Set(runpkg.KeyClosure, "other"))
	seen := 0
	for _, c := range set {
		if c.Key == runpkg.KeyClosure.Name {
			seen++
			if string(c.Value) != "other" {
				t.Fatalf("Set did not replace the row: %q", c.Value)
			}
		}
	}
	if seen != 1 {
		t.Fatalf("Set left %d closure rows, want one", seen)
	}
	// Set of an omittable row the defaults leave unemitted lands at the
	// writer's position — before the vouches, as GofreshEvidenceConfigs
	// orders the evidence — never appended.
	withPurity := recordingtest.Config(recordingtest.Set(runpkg.KeyPurity, "p"), recordingtest.Set(runpkg.KeyVouches, "v"))
	purityAt, vouchesAt := -1, -1
	for i, c := range withPurity {
		switch c.Key {
		case runpkg.KeyPurity.Name:
			purityAt = i
		case runpkg.KeyVouches.Name:
			vouchesAt = i
		}
	}
	if purityAt < 0 || vouchesAt < 0 || purityAt > vouchesAt {
		t.Fatalf("Set placed purity at %d and vouches at %d; want the writer's order", purityAt, vouchesAt)
	}
	// Omit of a row the writer never emitted is refused, never a silent
	// no-op claiming a predating shape.
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("Omit of an unemitted row was a silent no-op")
			}
		}()
		recordingtest.Config(recordingtest.Omit(runpkg.KeyPurity))
	}()
	// The format row is the writer's constant: removable, never settable;
	// a dirty flag that is not a boolean is refused, never recorded false.
	refuses := func(name string, opt recordingtest.Option) {
		t.Helper()
		defer func() {
			if recover() == nil {
				t.Fatalf("%s was accepted silently", name)
			}
		}()
		recordingtest.Config(opt)
	}
	refuses("Set(format)", recordingtest.Set(runpkg.KeyFormat, "3"))
	refuses("Set(dirty, yes)", recordingtest.Set(runpkg.KeyDirty, "yes"))
	if store.IsRecording(recordingtest.Results("BenchmarkX", []float64{1}, recordingtest.Omit(runpkg.KeyFormat))) {
		t.Fatal("a recording without the format row still reads as a recording")
	}
	// An omittable row set empty is absent, never an empty line — the
	// two derivation rows included (spec §5's class column).
	for _, key := range []runpkg.RecordingKey{runpkg.KeyClosureStrategy, runpkg.KeyDynamicState, runpkg.KeyVouches} {
		for _, c := range recordingtest.Config(recordingtest.Set(key, "")) {
			if c.Key == key.Name {
				t.Fatalf("%s set empty is an empty line, not an absent row", key.Name)
			}
		}
	}
	if !store.IsRecordingShape(recordingtest.Results("BenchmarkX", []float64{1}, recordingtest.Omit(runpkg.KeyDynamicState))) {
		t.Fatal("omitting an omittable row broke the shape")
	}
}

// configValue is the value of key's row in cfg, "" when absent.
func configValue(cfg []benchfmt.Config, key runpkg.RecordingKey) string {
	for _, c := range cfg {
		if c.Key == key.Name {
			return string(c.Value)
		}
	}
	return ""
}
