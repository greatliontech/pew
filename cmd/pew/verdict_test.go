package main

import (
	"context"
	"testing"

	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/pew/internal/recordingtest"
	runpkg "github.com/greatliontech/pew/internal/run"
	"github.com/greatliontech/pew/internal/store"
	"golang.org/x/perf/benchfmt"
)

// TestFingerprintFromConfig pins native fingerprint admission through the envelope.
func TestFingerprintFromConfig(t *testing.T) {
	cfg := recordingtest.Config(recordingtest.Set(runpkg.KeyToolchain, "tc"), recordingtest.Set(runpkg.KeyMachine, "m"), recordingtest.Set(runpkg.KeyBuildConfig, "bc"), recordingtest.Set(runpkg.KeyRuntimeConfig, "rc"), recordingtest.Set(runpkg.KeyClosure, "cl"), recordingtest.Set(runpkg.KeyTestVariants, "tv"), recordingtest.Set(runpkg.KeyTestVariantLedger, "ledger-encoded"), recordingtest.Set(runpkg.KeyRuntime, "rd"), recordingtest.Set(runpkg.KeyRuntimeInputs, "manifest"), recordingtest.Set(runpkg.KeyPurity, "source directive"))
	fp, recordedLedger, ok := fingerprintFromConfig(cfg)
	if !ok {
		t.Fatal("current recording format rejected")
	}
	if fp.MaximalClosure != "cl" || fp.TestVariantClosure != "tv" || fp.RuntimeInputs != "manifest" || fp.RuntimeDigest != "rd" || fp.PurityAssertion != "source directive" || fp.ResultKind != gofresh.Measurement {
		t.Errorf("fingerprint closure/runtime fields = %+v", fp)
	}
	if recordedLedger != "ledger-encoded" {
		t.Errorf("recorded ledger = %q, want %q", recordedLedger, "ledger-encoded")
	}
	g := fp.Guards
	if g.Toolchain != "tc" || g.BuildConfig != "bc" || g.Machine != "m" || g.RuntimeConfig != "rc" {
		t.Errorf("guards = %+v", g)
	}
	// Parsed configuration holds one entry per key (benchfmt's reader
	// replaces a repeated key in place), so a duplicated discriminator
	// never reaches this reader: the raw reader's rule marks the file
	// (store.rawFormatValid, pinned with the store), and this reader
	// judges the annotation it sets and the version.
	unknown := append([]benchfmt.Config(nil), cfg...)
	unknown[0].Value = []byte("1")
	annotated := append(append([]benchfmt.Config(nil), cfg...), benchfmt.Config{Key: runpkg.FormatInvalidAnnotation, Value: []byte("true")})
	for name, malformed := range map[string][]benchfmt.Config{"unknown": unknown, "raw-invalid annotated": annotated} {
		if _, _, ok := fingerprintFromConfig(malformed); ok {
			t.Errorf("%s recording format accepted", name)
		}
	}

	fp, _, ok = fingerprintFromConfig(nil)
	if ok || fp != (gofresh.Fingerprint{}) {
		t.Errorf("unversioned config: fp=%+v ok=%v, want rejection", fp, ok)
	}
}

func TestUnversionedRecordingIsStale(t *testing.T) {
	st := store.New(t.TempDir())
	recs := []*benchfmt.Result{{Name: benchfmt.Name("NoIO"), Iters: 1, Values: []benchfmt.Value{{Value: 1, Unit: "sec/op"}}, Config: []benchfmt.Config{{Key: "pew-runtime", Value: []byte("old")}}}}
	if err := st.Write("", "BenchmarkNoIO", "", recs); err != nil {
		t.Fatal(err)
	}
	v, reason, _, _, err := checkOne(context.Background(), st, nil, "example.com/old", "", "", "BenchmarkNoIO", "")
	if err != nil {
		t.Fatal(err)
	}
	if v != verdictStale || reason != "format" {
		t.Fatalf("unversioned recording = {%s %q}, want stale format", v, reason)
	}
	// A current-format recording missing mandatory rows: the commit, the
	// run conditions, the test-variant hash and ledger.
	incomplete := recordingtest.Results("NoIO", []float64{1},
		recordingtest.Omit(runpkg.KeyCommit), recordingtest.Omit(runpkg.KeyRunConditions),
		recordingtest.Omit(runpkg.KeyTestVariants), recordingtest.Omit(runpkg.KeyTestVariantLedger))
	if err := st.Write("", "BenchmarkNoIO", "incomplete", incomplete); err != nil {
		t.Fatal(err)
	}
	v, reason, _, _, err = checkOne(context.Background(), st, nil, "example.com/old", "", "", "BenchmarkNoIO", "incomplete")
	if err != nil {
		t.Fatal(err)
	}
	if v != verdictStale || reason != "format" {
		t.Fatalf("incomplete current-format recording = {%s %q}, want stale format", v, reason)
	}
}
