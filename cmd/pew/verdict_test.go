package main

import (
	"testing"

	gofresh "github.com/greatliontech/gofresh"
	runpkg "github.com/greatliontech/pew/internal/run"
	"github.com/greatliontech/pew/internal/store"
	"golang.org/x/perf/benchfmt"
)

// TestFingerprintFromConfig pins the config-line ↔ fingerprint mapping (spec §5:
// pew owns the serialization; gofresh owns the semantics).
func TestFingerprintFromConfig(t *testing.T) {
	cfg := []benchfmt.Config{
		{Key: "pew-format", Value: []byte(runpkg.RecordingFormat)},
		{Key: "commit", Value: []byte("c1")},
		{Key: "toolchain", Value: []byte("tc")},
		{Key: "machine", Value: []byte("m")},
		{Key: "buildconfig", Value: []byte("bc")},
		{Key: "runtimeconfig", Value: []byte("rc")},
		{Key: "pew-closure", Value: []byte("cl")},
		{Key: "pew-test-variants", Value: []byte("tv")},
		{Key: "pew-test-variant-ledger", Value: []byte("ledger-encoded")},
		{Key: "pew-runtime", Value: []byte("rd")},
		{Key: "pew-runtime-inputs", Value: []byte("manifest")},
		{Key: "pew-purity", Value: []byte("source directive")},
	}
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
	unknown := append([]benchfmt.Config(nil), cfg...)
	unknown[0].Value = []byte("1")
	duplicate := append(append([]benchfmt.Config(nil), cfg...), benchfmt.Config{Key: "pew-format", Value: []byte(runpkg.RecordingFormat)})
	for name, malformed := range map[string][]benchfmt.Config{"unknown": unknown, "duplicate": duplicate} {
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
	v, reason, _, _, err := checkOne(st, nil, "example.com/old", "", "", "BenchmarkNoIO", "")
	if err != nil {
		t.Fatal(err)
	}
	if v != verdictStale || reason != "format" {
		t.Fatalf("unversioned recording = {%s %q}, want stale format", v, reason)
	}
	incomplete := []*benchfmt.Result{{Name: benchfmt.Name("NoIO"), Iters: 1, Values: []benchfmt.Value{{Value: 1, Unit: "sec/op"}}, Config: []benchfmt.Config{
		{Key: "pew-format", Value: []byte(runpkg.RecordingFormat), File: true},
		{Key: "toolchain", Value: []byte("go"), File: true}, {Key: "machine", Value: []byte("m"), File: true},
		{Key: "buildconfig", Value: []byte("b"), File: true}, {Key: "runtimeconfig", Value: []byte("r"), File: true},
		{Key: "dirty", Value: []byte("false"), File: true}, {Key: "pew-closure", Value: []byte("c"), File: true},
		{Key: "pew-runtime", Value: []byte("d"), File: true}, {Key: "pew-runtime-inputs", Value: []byte("i"), File: true},
	}}}
	if err := st.Write("", "BenchmarkNoIO", "incomplete", incomplete); err != nil {
		t.Fatal(err)
	}
	v, reason, _, _, err = checkOne(st, nil, "example.com/old", "", "", "BenchmarkNoIO", "incomplete")
	if err != nil {
		t.Fatal(err)
	}
	if v != verdictStale || reason != "format" {
		t.Fatalf("incomplete current-format recording = {%s %q}, want stale format", v, reason)
	}
}
