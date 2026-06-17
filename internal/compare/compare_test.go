package compare

import (
	"bytes"
	"math"
	"sort"
	"strings"
	"testing"

	"golang.org/x/perf/benchfmt"
)

// sampleSet builds n result rows for one benchmark, one per measurement sample,
// carrying just a machine fingerprint. units maps a unit to its per-sample
// values; all slices must share a length.
func sampleSet(name, machine string, units map[string][]float64) []*benchfmt.Result {
	cfg := map[string]string{}
	if machine != "" {
		cfg["machine"] = machine
	}
	cfg["toolchain"] = "go-test"
	cfg["buildconfig"] = "build-test"
	return benchResults(name, cfg, units)
}

// benchResults builds n result rows for one benchmark with arbitrary file config.
func benchResults(name string, cfg map[string]string, units map[string][]float64) []*benchfmt.Result {
	n := -1
	for _, vs := range units {
		if n == -1 {
			n = len(vs)
		} else if len(vs) != n {
			panic("benchResults: ragged unit slices")
		}
	}
	// Deterministic config order.
	var keys []string
	for k := range cfg {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var rs []*benchfmt.Result
	for i := range n {
		var vals []benchfmt.Value
		for _, u := range []string{"sec/op", "B/op", "allocs/op"} {
			if vs, ok := units[u]; ok {
				vals = append(vals, benchfmt.Value{Value: vs[i], Unit: u})
			}
		}
		var config []benchfmt.Config
		for _, k := range keys {
			config = append(config, benchfmt.Config{Key: k, Value: []byte(cfg[k]), File: true})
		}
		rs = append(rs, &benchfmt.Result{Name: benchfmt.Name(name), Iters: 1, Values: vals, Config: config})
	}
	return rs
}

// seq returns [start, start+1, …, start+n-1] — a tight, fully-orderable spread.
func seq(start float64, n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = start + float64(i)
	}
	return out
}

// rep returns n copies of v.
func rep(v float64, n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = v
	}
	return out
}

// alternate returns [a, b, a, b, …] of length 2*pairs.
func alternate(a, b float64, pairs int) []float64 {
	out := make([]float64, 0, 2*pairs)
	for range pairs {
		out = append(out, a, b)
	}
	return out
}

func secRow(t *testing.T, res *Result) Row {
	t.Helper()
	for _, ut := range res.Tables {
		if ut.Unit == "sec/op" {
			if len(ut.Rows) != 1 {
				t.Fatalf("want exactly one sec/op row, got %d", len(ut.Rows))
			}
			return ut.Rows[0]
		}
	}
	t.Fatalf("no sec/op table in result (%d units)", len(res.Tables))
	return Row{}
}

// TestRegressionCriterion encodes the §10.1 invariant: a regression requires all
// three of worse-direction, statistical significance (p<α), and magnitude ≥
// threshold. Each case drops exactly one condition and must NOT be a regression;
// the all-three case must be.
func TestRegressionCriterion(t *testing.T) {
	opts := DefaultOptions() // α=0.05, threshold=3%

	tests := []struct {
		name           string
		base, newer    []float64
		wantRegression bool
		wantSignif     bool // p < α — asserted so cases aren't vacuously passing
		deltaSign      int  // expected sign of DeltaPct: +1, -1, 0
	}{
		{
			// worse + significant + ~10% ≥ 3%  → regression
			name: "all three hold", base: seq(1000, 8), newer: seq(1100, 8),
			wantRegression: true, wantSignif: true, deltaSign: +1,
		},
		{
			// worse + significant but ~1.5% < 3% → not a regression (magnitude floor)
			name: "magnitude below threshold", base: seq(1000, 8), newer: seq(1015, 8),
			wantRegression: false, wantSignif: true, deltaSign: +1,
		},
		{
			// significant + ~9% but FASTER → not a regression (wrong direction)
			name: "improvement, not regression", base: seq(1100, 8), newer: seq(1000, 8),
			wantRegression: false, wantSignif: true, deltaSign: -1,
		},
		{
			// worse + ~6.7% ≥ 3% but heavy overlap → p ≥ α → not a regression
			name: "magnitude high but not significant",
			base: alternate(100, 200, 4), newer: alternate(110, 210, 4),
			wantRegression: false, wantSignif: false, deltaSign: +1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := Compare(
				sampleSet("BenchmarkX-8", "m1", map[string][]float64{"sec/op": tt.base}),
				sampleSet("BenchmarkX-8", "m1", map[string][]float64{"sec/op": tt.newer}),
				opts,
			)
			row := secRow(t, res)
			if row.Regression != tt.wantRegression {
				t.Errorf("Regression = %v, want %v (p=%g delta=%.2f%%)", row.Regression, tt.wantRegression, row.Cmp.P, row.DeltaPct)
			}
			gotSignif := row.Cmp.P < opts.Alpha
			if gotSignif != tt.wantSignif {
				t.Errorf("significant = %v (p=%g), want %v", gotSignif, row.Cmp.P, tt.wantSignif)
			}
			if got := sign(row.DeltaPct); got != tt.deltaSign {
				t.Errorf("DeltaPct sign = %d (%.2f%%), want %d", got, row.DeltaPct, tt.deltaSign)
			}
		})
	}
}

func sign(x float64) int {
	switch {
	case x > 0:
		return 1
	case x < 0:
		return -1
	default:
		return 0
	}
}

// TestDistinctPackagesNotMerged encodes the §10.1 grouping invariant: the same
// benchmark name in two different packages must NOT be merged into one comparison
// — the native `pkg` config keeps them apart even though pew provenance keys are
// projected away.
func TestDistinctPackagesNotMerged(t *testing.T) {
	// Same benchmark name, two packages. Package a regresses (1000→1100); package
	// b is flat (1000→1000). A merge would average them and hide a's regression.
	cfgA := map[string]string{"pkg": "example.com/a", "machine": "m1", "toolchain": "go-test", "buildconfig": "build-test"}
	cfgB := map[string]string{"pkg": "example.com/b", "machine": "m1", "toolchain": "go-test", "buildconfig": "build-test"}
	base := append(
		benchResults("BenchmarkParse-8", cfgA, map[string][]float64{"sec/op": seq(1000, 8)}),
		benchResults("BenchmarkParse-8", cfgB, map[string][]float64{"sec/op": seq(1000, 8)})...,
	)
	newer := append(
		benchResults("BenchmarkParse-8", cfgA, map[string][]float64{"sec/op": seq(1100, 8)}),
		benchResults("BenchmarkParse-8", cfgB, map[string][]float64{"sec/op": seq(1000, 8)})...,
	)

	res := Compare(base, newer, DefaultOptions())

	// Two distinct sec/op tables (one per package), each with exactly one row.
	var aReg, bReg, secTables int
	for _, tbl := range res.Tables {
		if tbl.Unit != "sec/op" {
			continue
		}
		secTables++
		if len(tbl.Rows) != 1 {
			t.Fatalf("config %q: got %d rows, want 1 (merge?)", tbl.Config, len(tbl.Rows))
		}
		switch {
		case strings.Contains(tbl.Config, "example.com/a"):
			if tbl.Rows[0].Regression {
				aReg++
			}
		case strings.Contains(tbl.Config, "example.com/b"):
			if tbl.Rows[0].Regression {
				bReg++
			}
		default:
			t.Errorf("unexpected config %q", tbl.Config)
		}
	}
	if secTables != 2 {
		t.Fatalf("got %d sec/op tables, want 2 (one per package)", secTables)
	}
	if aReg != 1 {
		t.Error("package a's regression was not detected (merged away?)")
	}
	if bReg != 0 {
		t.Error("package b (flat) was flagged as a regression")
	}
}

func TestPewRuntimeKeysDoNotFragmentComparison(t *testing.T) {
	baseCfg := map[string]string{
		"pkg":                "example.com/a",
		"machine":            "m1",
		"toolchain":          "go-test",
		"buildconfig":        "build-test",
		"pew-runtime":        "old-runtime",
		"pew-runtime-inputs": "old-manifest",
	}
	newCfg := map[string]string{
		"pkg":                "example.com/a",
		"machine":            "m1",
		"toolchain":          "go-test",
		"buildconfig":        "build-test",
		"pew-runtime":        "new-runtime",
		"pew-runtime-inputs": "new-manifest",
	}
	res := Compare(
		benchResults("BenchmarkParse-8", baseCfg, map[string][]float64{"sec/op": seq(1000, 8)}),
		benchResults("BenchmarkParse-8", newCfg, map[string][]float64{"sec/op": seq(1100, 8)}),
		DefaultOptions(),
	)

	var secTables int
	for _, tbl := range res.Tables {
		if tbl.Unit != "sec/op" {
			continue
		}
		secTables++
		if strings.Contains(tbl.Config, "pew-runtime") {
			t.Fatalf("runtime metadata leaked into comparison config: %q", tbl.Config)
		}
		if len(tbl.Rows) != 1 || !tbl.Rows[0].Regression {
			t.Fatalf("runtime keys fragmented comparison; rows=%d regression=%v", len(tbl.Rows), len(tbl.Rows) == 1 && tbl.Rows[0].Regression)
		}
	}
	if secTables != 1 {
		t.Fatalf("got %d sec/op tables, want 1", secTables)
	}
}

// TestMachineGuard encodes the §8/§10 invariant: differing machine fingerprints
// are never compared silently — surfaced as a note, with no comparison row.
func TestMachineGuard(t *testing.T) {
	base := sampleSet("BenchmarkX-8", "machineA", map[string][]float64{"sec/op": seq(1000, 8)})
	newer := sampleSet("BenchmarkX-8", "machineB", map[string][]float64{"sec/op": seq(1100, 8)})

	res := Compare(base, newer, DefaultOptions())
	if len(res.Tables) != 0 {
		t.Errorf("compared across machines: got %d unit tables, want 0", len(res.Tables))
	}
	if len(res.Notes) != 1 || !strings.Contains(res.Notes[0], "machine mismatch") {
		t.Errorf("notes = %v, want one machine-mismatch note", res.Notes)
	}
	if res.Regressed() {
		t.Error("Regressed() = true across a machine mismatch; want false")
	}

	// Same machine → it compares (and, here, regresses).
	same := Compare(base, sampleSet("BenchmarkX-8", "machineA", map[string][]float64{"sec/op": seq(1100, 8)}), DefaultOptions())
	if len(same.Tables) == 0 {
		t.Fatal("same-machine comparison produced no tables")
	}
	if !secRow(t, same).Regression {
		t.Error("same-machine 10% slowdown not flagged as regression")
	}
}

func TestVariantGuards(t *testing.T) {
	baseCfg := map[string]string{"pkg": "p", "machine": "m1", "toolchain": "go1", "buildconfig": "b1"}
	for _, tc := range []struct {
		name     string
		newCfg   map[string]string
		wantNote string
	}{
		{"toolchain mismatch", map[string]string{"pkg": "p", "machine": "m1", "toolchain": "go2", "buildconfig": "b1"}, "toolchain mismatch"},
		{"buildconfig mismatch", map[string]string{"pkg": "p", "machine": "m1", "toolchain": "go1", "buildconfig": "b2"}, "buildconfig mismatch"},
		{"missing machine", map[string]string{"pkg": "p", "toolchain": "go1", "buildconfig": "b1"}, "missing machine"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := Compare(
				benchResults("BenchmarkX-8", baseCfg, map[string][]float64{"sec/op": seq(1000, 8)}),
				benchResults("BenchmarkX-8", tc.newCfg, map[string][]float64{"sec/op": seq(1100, 8)}),
				DefaultOptions(),
			)
			if len(res.Tables) != 0 {
				t.Fatalf("compared across variant guard mismatch: got %d tables", len(res.Tables))
			}
			if len(res.Notes) != 1 || !strings.Contains(res.Notes[0], tc.wantNote) {
				t.Fatalf("notes = %v, want %q", res.Notes, tc.wantNote)
			}
		})
	}
}

// TestMixedMachineWithinSide: if one side carries two distinct machine
// fingerprints for the same benchmark (hand-edited / externally-merged input), the
// guard refuses rather than silently folding them — the §8/§10 contract holds
// unconditionally, not just for the single-machine-per-file in-spec case.
func TestMixedMachineWithinSide(t *testing.T) {
	cfgA := map[string]string{"pkg": "p", "machine": "machineA", "toolchain": "go-test", "buildconfig": "build-test"}
	cfgB := map[string]string{"pkg": "p", "machine": "machineB", "toolchain": "go-test", "buildconfig": "build-test"}
	// Base side mixes machineA and machineB for the same benchmark; pkg is equal so
	// they fall in one group (machine is projected away from grouping).
	base := append(
		benchResults("BenchmarkX-8", cfgA, map[string][]float64{"sec/op": seq(1000, 8)}),
		benchResults("BenchmarkX-8", cfgB, map[string][]float64{"sec/op": seq(1000, 8)})...,
	)
	newer := benchResults("BenchmarkX-8", cfgA, map[string][]float64{"sec/op": seq(1100, 8)})

	res := Compare(base, newer, DefaultOptions())
	if len(res.Tables) != 0 {
		t.Errorf("compared a mixed-machine side: got %d tables, want 0", len(res.Tables))
	}
	if len(res.Notes) != 1 || !strings.Contains(res.Notes[0], "mixed machine") {
		t.Errorf("notes = %v, want one mixed-machine note", res.Notes)
	}
}

// TestGatingScopesExit encodes the entailed invariant: --fail-on-regression
// (Regressed) reflects only gated units. A B/op-only regression must not fail the
// build under the default gate (sec/op), though it is still flagged in the table.
func TestGatingScopesExit(t *testing.T) {
	// sec/op identical both sides (no time regression); B/op +20% (a regression).
	base := sampleSet("BenchmarkX-8", "m1", map[string][]float64{
		"sec/op": rep(1000, 8), "B/op": rep(1000, 8),
	})
	newer := sampleSet("BenchmarkX-8", "m1", map[string][]float64{
		"sec/op": rep(1000, 8), "B/op": rep(1200, 8),
	})

	def := Compare(base, newer, DefaultOptions())
	if def.Regressed() {
		t.Error("default gate (sec/op) fails the build on a B/op-only regression; want false")
	}
	if !bytesRowRegressed(def) {
		t.Error("B/op +20% not flagged as a regression in the table")
	}

	// Gate on B/op → the same regression now fails the build.
	opts := DefaultOptions()
	opts.GateUnits = map[string]bool{"B/op": true}
	if !Compare(base, newer, opts).Regressed() {
		t.Error("gating on B/op did not fail the build on a B/op regression")
	}
}

func bytesRowRegressed(res *Result) bool {
	for _, ut := range res.Tables {
		if ut.Unit == "B/op" {
			for _, row := range ut.Rows {
				if row.Regression {
					return true
				}
			}
		}
	}
	return false
}

// TestOneSidedNotCompared: a benchmark present on only one side is surfaced, not
// compared and not a regression.
func TestOneSidedNotCompared(t *testing.T) {
	base := sampleSet("BenchmarkOnlyBase-8", "m1", map[string][]float64{"sec/op": seq(1000, 8)})
	res := Compare(base, nil, DefaultOptions())
	if len(res.Tables) != 0 {
		t.Errorf("one-sided benchmark produced %d tables, want 0", len(res.Tables))
	}
	if len(res.Notes) != 1 || !strings.Contains(res.Notes[0], "only present in base") {
		t.Errorf("notes = %v, want one 'only present in base' note", res.Notes)
	}

	res = Compare(nil, base, DefaultOptions())
	if len(res.Notes) != 1 || !strings.Contains(res.Notes[0], "only present in new") {
		t.Errorf("notes = %v, want one 'only present in new' note", res.Notes)
	}
}

// TestThroughputUnitNotJudged: a unit with no defined worse-direction (B/s, where
// higher is better) is shown but never flagged as a regression.
func TestThroughputUnitNotJudged(t *testing.T) {
	// B/s is higher-is-better: a larger new value is an *improvement*, not a
	// regression, and pew makes no judgement on it.
	base := withUnit("BenchmarkX-8", "m1", "B/s", seq(1000, 8))
	newer := withUnit("BenchmarkX-8", "m1", "B/s", seq(1200, 8))

	res := Compare(base, newer, DefaultOptions())
	for _, ut := range res.Tables {
		if ut.Unit != "B/s" {
			continue
		}
		for _, row := range ut.Rows {
			if row.Regression {
				t.Error("throughput unit B/s flagged as a regression; direction is undefined for it")
			}
		}
		return
	}
	t.Fatal("B/s unit table missing")
}

func withUnit(name, machine, unit string, vals []float64) []*benchfmt.Result {
	var rs []*benchfmt.Result
	for _, v := range vals {
		rs = append(rs, &benchfmt.Result{
			Name:   benchfmt.Name(name),
			Iters:  1,
			Values: []benchfmt.Value{{Value: v, Unit: unit}},
			Config: []benchfmt.Config{
				{Key: "machine", Value: []byte(machine), File: true},
				{Key: "toolchain", Value: []byte("go-test"), File: true},
				{Key: "buildconfig", Value: []byte("build-test"), File: true},
			},
		})
	}
	return rs
}

// TestNaNDeltaOnZeroBaseline: a zero baseline center yields a NaN delta and never
// a regression (the magnitude is undefined, not "infinitely worse").
func TestNaNDeltaOnZeroBaseline(t *testing.T) {
	base := sampleSet("BenchmarkX-8", "m1", map[string][]float64{"allocs/op": rep(0, 8)})
	newer := sampleSet("BenchmarkX-8", "m1", map[string][]float64{"allocs/op": rep(5, 8)})
	res := Compare(base, newer, Options{Alpha: 0.05, ThresholdPct: 3, Confidence: 0.95, GateUnits: map[string]bool{"allocs/op": true}})
	for _, ut := range res.Tables {
		if ut.Unit != "allocs/op" {
			continue
		}
		row := ut.Rows[0]
		if !math.IsNaN(row.DeltaPct) {
			t.Errorf("DeltaPct = %v on zero baseline, want NaN", row.DeltaPct)
		}
		if row.Regression {
			t.Error("regression flagged on an undefined (zero-baseline) delta")
		}
		return
	}
	t.Fatal("allocs/op table missing")
}

func TestWriteTextMarksRegression(t *testing.T) {
	res := Compare(
		sampleSet("BenchmarkX-8", "m1", map[string][]float64{"sec/op": seq(1000, 8)}),
		sampleSet("BenchmarkX-8", "m1", map[string][]float64{"sec/op": seq(1100, 8)}),
		DefaultOptions(),
	)
	var buf bytes.Buffer
	if err := res.WriteText(&buf); err != nil {
		t.Fatalf("WriteText: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"sec/op", "BenchmarkX-8", "⚠ regression", "vs base"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}
