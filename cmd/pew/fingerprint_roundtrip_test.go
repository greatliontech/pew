package main

import (
	"reflect"
	"testing"

	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/guard"
	runpkg "github.com/greatliontech/pew/internal/run"
)

// Every native fingerprint field survives the envelope without reconstruction.
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
		ClosureStrategy:          "gofresh/closure@1+canonical@2+ledger@1",
		RuntimeInputs:            "manifest-encoded",
		RuntimeDigest:            "manifest-digest",
		ResultKind:               gofresh.Measurement,
		ObservationAssertion:     "observed-by-producer",
		ObservationProof:         gofresh.ObservationProof{Strategy: "proof-strategy", Subject: gofresh.Subject{Package: "example/p", Symbol: "BenchmarkX"}, Observable: true, Evidence: "integrity-evidence"},
	}

	// A new native field must join this full-field seed, including evidence
	// whose semantic integrity is judged later by the shared checker.
	v := reflect.ValueOf(want)
	for i := range v.NumField() {
		name := v.Type().Field(i).Name
		if v.Field(i).IsZero() {
			t.Errorf("fingerprint field %s is zero in the round-trip seed", name)
		}
	}

	fpcfg, err := runpkg.FingerprintConfigs(want, "ledger-encoded")
	if err != nil {
		t.Fatal(err)
	}
	cfg := append(
		runpkg.ProvenanceConfig("c1", false, runpkg.Conditions{}),
		fpcfg...,
	)
	got, ledger, ok := fingerprintFromConfig(cfg)
	if !ok {
		t.Fatal("fingerprintFromConfig rejected the writer's own output")
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip diverged:\n got %+v\nwant %+v", got, want)
	}
	if ledger != "ledger-encoded" {
		t.Errorf("ledger = %q, want %q", ledger, "ledger-encoded")
	}
}
