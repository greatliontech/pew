package main

import (
	"testing"

	gofresh "github.com/greatliontech/gofresh"
	runpkg "github.com/greatliontech/pew/internal/run"
	"golang.org/x/perf/benchfmt"
)

// admissionConfig is a current-shape recording configuration recorded
// under strategy.
func admissionConfig(strategy string) []benchfmt.Config {
	return []benchfmt.Config{
		{Key: "pew-format", Value: []byte(runpkg.RecordingFormat), File: true},
		{Key: "commit", Value: []byte("c1"), File: true},
		{Key: "toolchain", Value: []byte("tc"), File: true},
		{Key: "machine", Value: []byte("m"), File: true},
		{Key: "buildconfig", Value: []byte("bc"), File: true},
		{Key: "runtimeconfig", Value: []byte("rc"), File: true},
		{Key: "pew-closure", Value: []byte("cl"), File: true},
		{Key: "pew-dynamic-state", Value: []byte(strategy), File: true},
		{Key: "pew-test-variants", Value: []byte("tv"), File: true},
		{Key: "pew-test-variant-ledger", Value: []byte("lg"), File: true},
		{Key: "pew-runtime", Value: []byte("rd"), File: true},
		{Key: "pew-runtime-inputs", Value: []byte("manifest"), File: true},
		{Key: "dirty", Value: []byte("false"), File: true},
		{Key: "pew-runconditions", Value: []byte("governor=performance turbo=off load1=0.03 throttled=false battery=false"), File: true},
	}
}

func admissionRow(cfg []benchfmt.Config) *benchfmt.Result {
	return &benchfmt.Result{Name: benchfmt.Name("X"), Iters: 1, Values: []benchfmt.Value{{Value: 1, Unit: "sec/op"}}, Config: cfg}
}

// One admissibility ladder for every verdict surface (REQ-pew-admission):
// the format rung judges the whole recording — a row disagreeing past
// row 0 refuses as format, never surfacing later as a mixed-provenance
// note — and the strategy rung applies to a working-tree side alone: a
// ref-resolved side compares under any strategy.
func TestAdmitRecordingClimbsOneLadderOverTheWholeRecording(t *testing.T) {
	current := admissionConfig(gofresh.DynamicStateStrategy)
	if adm := admitRecording([]*benchfmt.Result{admissionRow(current)}, true); !adm.ok || adm.fp.MaximalClosure != "cl" || adm.ledger != "lg" {
		t.Fatalf("current recording refused: %+v", adm)
	}
	other := admissionConfig("another-strategy")
	if adm := admitRecording([]*benchfmt.Result{admissionRow(other)}, true); adm.ok || adm.class != "dynamic-state strategy" || adm.fp.DynamicStateStrategy != "another-strategy" {
		t.Fatalf("working-tree side under another strategy = %+v, want the strategy class with the decoded fingerprint", adm)
	}
	if adm := admitRecording([]*benchfmt.Result{admissionRow(other)}, false); !adm.ok {
		t.Fatalf("ref-resolved side under another strategy refused: %+v", adm)
	}
	if adm := admitRecording(nil, true); adm.ok || adm.class != "format" {
		t.Fatalf("empty recording = %+v, want format", adm)
	}
	unversioned := append([]benchfmt.Config(nil), current[1:]...)
	if adm := admitRecording([]*benchfmt.Result{admissionRow(unversioned)}, false); adm.ok || adm.class != "format" {
		t.Fatalf("unversioned recording = %+v, want format", adm)
	}
	// Row 0 passes; row 1 disagrees — a different closure, then a
	// missing key: the whole recording refuses.
	divergent := admissionConfig(gofresh.DynamicStateStrategy)
	for i := range divergent {
		if divergent[i].Key == "pew-closure" {
			divergent[i].Value = []byte("other-closure")
		}
	}
	if adm := admitRecording([]*benchfmt.Result{admissionRow(current), admissionRow(divergent)}, false); adm.ok || adm.class != "format" {
		t.Fatalf("rows disagreeing on the fingerprint = %+v, want format", adm)
	}
	var shapeless []benchfmt.Config
	for _, c := range current {
		if c.Key != "pew-runtime" {
			shapeless = append(shapeless, c)
		}
	}
	if adm := admitRecording([]*benchfmt.Result{admissionRow(current), admissionRow(shapeless)}, false); adm.ok || adm.class != "format" {
		t.Fatalf("a later row missing a key = %+v, want format", adm)
	}
	if adm := admitRecording([]*benchfmt.Result{admissionRow(current), admissionRow(current)}, true); !adm.ok {
		t.Fatalf("agreeing rows refused: %+v", adm)
	}
	// A key outside the fingerprint (dirty) disagreeing past row 0 is the
	// same refusal: the closed set is judged whole.
	dirtier := admissionConfig(gofresh.DynamicStateStrategy)
	for i := range dirtier {
		if dirtier[i].Key == "dirty" {
			dirtier[i].Value = []byte("true")
		}
	}
	if adm := admitRecording([]*benchfmt.Result{admissionRow(current), admissionRow(dirtier)}, false); adm.ok || adm.class != "format" {
		t.Fatalf("rows disagreeing on dirty = %+v, want format", adm)
	}
	// Strategy outranks the engine's closure reason: a recording stale on
	// both refuses at the strategy rung, before any engine verdict.
	both := admissionConfig("another-strategy")
	for i := range both {
		if both[i].Key == "pew-closure" {
			both[i].Value = []byte("moved-closure")
		}
	}
	if adm := admitRecording([]*benchfmt.Result{admissionRow(both)}, true); adm.ok || adm.class != "dynamic-state strategy" {
		t.Fatalf("stale on strategy and closure = %+v, want the strategy class", adm)
	}
}
