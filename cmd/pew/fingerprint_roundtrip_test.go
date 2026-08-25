package main

import (
	"reflect"
	"testing"

	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/guard"
	runpkg "github.com/greatliontech/pew/internal/run"
)

// The writer-side enumeration (fingerprintConfigs + ProvenanceConfig)
// and the reader-side map (fingerprintFromConfig) are a matched pair:
// every recorded fingerprint field must survive the write→read round
// trip, so a key dropped on either side fails here instead of silently
// narrowing the verdict evidence.
func TestFingerprintConfigRoundTrip(t *testing.T) {
	want := gofresh.Fingerprint{
		MaximalClosure:     "closure-hash",
		TestVariantClosure: "variant-hash",
		Guards: guard.Guards{
			Toolchain:     "go-toolchain",
			Machine:       "machine-fp",
			BuildConfig:   "build-digest",
			RuntimeConfig: "runtime-digest",
		},
		PurityAssertion:          "purity-attribution",
		DynamicStateVouches:      "a.example/dep.Var",
		SingleSubjectDischarges:  "s.example/dep.One",
		PackageProcessDischarges: "p.example/dep.Two",
		DynamicStateStrategy:     "gofresh/dynamic-state@34",
		RuntimeInputs:            "manifest-encoded",
		RuntimeDigest:            "manifest-digest",
		ResultKind:               gofresh.Measurement,
	}

	// Fields the recording deliberately does not carry: observation
	// evidence is recomputed at judge time, never served from the
	// record. Everything else must be non-zero above — a new
	// gofresh.Fingerprint field lands here unset, fails this walk, and
	// forces the record-or-exempt decision instead of a silent gap.
	exempt := map[string]bool{"ObservationAssertion": true, "ObservationProof": true}
	v := reflect.ValueOf(want)
	for i := range v.NumField() {
		name := v.Type().Field(i).Name
		if exempt[name] {
			continue
		}
		if v.Field(i).IsZero() {
			t.Errorf("fingerprint field %s is zero in the round-trip seed: record it (writer+reader+this test) or exempt it here with the reason", name)
		}
	}

	cfg := append(
		runpkg.ProvenanceConfig("c1", false, want.Guards, runpkg.Conditions{}),
		fingerprintConfigs(want, "ledger-encoded", want.RuntimeDigest, want.RuntimeInputs)...,
	)
	got, pure, ledger, ok := fingerprintFromConfig(cfg)
	if !ok {
		t.Fatal("fingerprintFromConfig rejected the writer's own output")
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip diverged:\n got %+v\nwant %+v", got, want)
	}
	if ledger != "ledger-encoded" {
		t.Errorf("ledger = %q, want %q", ledger, "ledger-encoded")
	}
	if pure != "" {
		t.Errorf("pure = %q, want unset (PureConfig is a separate per-benchmark line)", pure)
	}
}
