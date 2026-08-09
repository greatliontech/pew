package compare

import (
	"strings"
	"testing"

	"golang.org/x/perf/benchfmt"
)

func vouchSet(name, vouches string, units map[string][]float64) []*benchfmt.Result {
	cfg := map[string]string{
		"pkg":           "p",
		"machine":       "m1",
		"toolchain":     "go-test",
		"buildconfig":   "build-test",
		"runtimeconfig": "runtime-test",
	}
	if vouches != "" {
		cfg["pew-vouches"] = vouches
	}
	return benchResults(name, cfg, units)
}

// Two sides measured under different dynamic-state acceptances compare
// WITH a note, never fragmenting - the run-conditions precedent
// (spec §5 pew-vouches row).
func TestVouchesNoteStillCompares(t *testing.T) {
	res := Compare(
		vouchSet("BenchmarkX-8", "", map[string][]float64{"sec/op": seq(1000, 8)}),
		vouchSet("BenchmarkX-8", "a.example/dep.Var", map[string][]float64{"sec/op": seq(1100, 8)}),
		DefaultOptions(),
	)
	if len(res.Notes) != 1 || !strings.Contains(res.Notes[0], "dynamic-state vouches differ") {
		t.Fatalf("notes = %v, want one vouches-differ note", res.Notes)
	}
	if !strings.Contains(res.Notes[0], "(none)") || !strings.Contains(res.Notes[0], "a.example/dep.Var") {
		t.Errorf("note %q does not name both sides", res.Notes[0])
	}
	_ = secRow(t, res) // fails if the key fragmented grouping or the note blocked comparison

	same := Compare(
		vouchSet("BenchmarkX-8", "", map[string][]float64{"sec/op": seq(1000, 8)}),
		vouchSet("BenchmarkX-8", "", map[string][]float64{"sec/op": seq(1100, 8)}),
		DefaultOptions(),
	)
	for _, note := range same.Notes {
		if strings.Contains(note, "vouches") {
			t.Fatalf("both-unvouched comparison noted vouches: %v", same.Notes)
		}
	}

	partial := append(
		vouchSet("BenchmarkX-8", "a.example/dep.Var", map[string][]float64{"sec/op": seq(1000, 4)}),
		vouchSet("BenchmarkX-8", "", map[string][]float64{"sec/op": seq(1005, 4)})...,
	)
	mixed := Compare(partial,
		vouchSet("BenchmarkX-8", "", map[string][]float64{"sec/op": seq(1100, 8)}),
		DefaultOptions(),
	)
	var haveMixed bool
	for _, note := range mixed.Notes {
		if strings.Contains(note, "mixed dynamic-state vouches within the base side") {
			haveMixed = true
		}
	}
	if !haveMixed {
		t.Fatalf("partially recorded side not reported as mixed: %v", mixed.Notes)
	}
}
