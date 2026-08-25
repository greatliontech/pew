package compare

import (
	"strings"
	"testing"

	"golang.org/x/perf/benchfmt"
)

func auditSet(name string, audit map[string]string, units map[string][]float64) []*benchfmt.Result {
	cfg := map[string]string{
		"pkg":           "p",
		"machine":       "m1",
		"toolchain":     "go-test",
		"buildconfig":   "build-test",
		"runtimeconfig": "runtime-test",
	}
	for k, v := range audit {
		cfg[k] = v
	}
	return benchResults(name, cfg, units)
}

// Two sides recorded under different gofresh provenance — dynamic-state
// strategy or attestation-borne discharge sets — compare WITH a note,
// never fragmenting (spec §5's audit rows: "exactly as pew-vouches").
func TestAuditNotesStillCompare(t *testing.T) {
	cases := []struct {
		key      string
		baseVal  string
		newVal   string
		wantNote string
	}{
		{"pew-dynamic-state", "gofresh/dynamic-state@33", "gofresh/dynamic-state@34", "dynamic-state strategies differ"},
		{"pew-single-subject-discharges", "", "a.example/dep.Var", "single-subject discharges differ"},
		{"pew-package-process-discharges", "", "b.example/dep.Other", "package-process discharges differ"},
	}
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			base := map[string]string{}
			if tc.baseVal != "" {
				base[tc.key] = tc.baseVal
			}
			newer := map[string]string{}
			if tc.newVal != "" {
				newer[tc.key] = tc.newVal
			}
			res := Compare(
				auditSet("BenchmarkX-8", base, map[string][]float64{"sec/op": seq(1000, 8)}),
				auditSet("BenchmarkX-8", newer, map[string][]float64{"sec/op": seq(1100, 8)}),
				DefaultOptions(),
			)
			if len(res.Notes) != 1 || !strings.Contains(res.Notes[0], tc.wantNote) {
				t.Fatalf("notes = %v, want one %q note", res.Notes, tc.wantNote)
			}
			if tc.baseVal != "" && !strings.Contains(res.Notes[0], tc.baseVal) {
				t.Errorf("note %q does not name the base value", res.Notes[0])
			}
			if tc.baseVal == "" && !strings.Contains(res.Notes[0], "(none)") {
				t.Errorf("note %q does not name the absent base side", res.Notes[0])
			}
			if !strings.Contains(res.Notes[0], tc.newVal) {
				t.Errorf("note %q does not name the new value", res.Notes[0])
			}
			_ = secRow(t, res) // fails if the key fragmented grouping or blocked comparison
		})
	}

	same := Compare(
		auditSet("BenchmarkX-8", map[string]string{"pew-dynamic-state": "gofresh/dynamic-state@34"}, map[string][]float64{"sec/op": seq(1000, 8)}),
		auditSet("BenchmarkX-8", map[string]string{"pew-dynamic-state": "gofresh/dynamic-state@34"}, map[string][]float64{"sec/op": seq(1100, 8)}),
		DefaultOptions(),
	)
	if len(same.Notes) != 0 {
		t.Fatalf("identical provenance comparison emitted notes: %v", same.Notes)
	}
}

// A side where some samples carry a provenance line and some omit it is
// mixed provenance and reports as such — a partially-recorded side can
// never silently read as one value.
func TestAuditNotesMixedSide(t *testing.T) {
	partial := append(
		auditSet("BenchmarkX-8", map[string]string{"pew-single-subject-discharges": "a.example/dep.Var"}, map[string][]float64{"sec/op": seq(1000, 4)}),
		auditSet("BenchmarkX-8", nil, map[string][]float64{"sec/op": seq(1005, 4)})...,
	)
	mixed := Compare(partial,
		auditSet("BenchmarkX-8", nil, map[string][]float64{"sec/op": seq(1100, 8)}),
		DefaultOptions(),
	)
	var haveMixed bool
	for _, note := range mixed.Notes {
		if strings.Contains(note, "mixed single-subject discharges within the base side") {
			haveMixed = true
		}
	}
	if !haveMixed {
		t.Fatalf("partially recorded side not reported as mixed: %v", mixed.Notes)
	}

	// Both sides mixed names both — a base-only attribution would hide
	// the new side's inconsistency.
	partialNew := append(
		auditSet("BenchmarkX-8", map[string]string{"pew-single-subject-discharges": "b.example/dep.Var"}, map[string][]float64{"sec/op": seq(1100, 4)}),
		auditSet("BenchmarkX-8", nil, map[string][]float64{"sec/op": seq(1105, 4)})...,
	)
	both := Compare(partial, partialNew, DefaultOptions())
	var haveBoth bool
	for _, note := range both.Notes {
		if strings.Contains(note, "mixed single-subject discharges within both sides") {
			haveBoth = true
		}
	}
	if !haveBoth {
		t.Fatalf("both-sides-mixed not attributed to both: %v", both.Notes)
	}
}
