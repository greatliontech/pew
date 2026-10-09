package compare

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/greatliontech/pew/internal/metric"
	"golang.org/x/perf/benchfmt"
)

func TestCoverageRetainsEveryRequestedUnit(t *testing.T) {
	o := DefaultOptions()
	o.GateUnits = map[string]bool{"sec/op": true, "B/op": true, "allocs/op": true}
	b := condSet("BenchmarkX/sub-8", quietConds, map[string][]float64{"sec/op": rep(1, 8), "B/op": rep(1, 8)})
	n := condSet("BenchmarkX/sub-8", quietConds, map[string][]float64{"sec/op": rep(1, 8)})
	r := Compare(b, n, o)
	c := r.Coverage(Policies{})
	if c.Requested != 3 || c.Compared != 1 || c.Eligible != 1 || c.Missing != 2 || !c.Satisfied {
		t.Fatalf("coverage %+v", c)
	}
	if r.Coverage(Policies{Coverage: "complete"}).Satisfied {
		t.Fatal("complete coverage ignored absent units")
	}
	for _, d := range r.Dispositions {
		if d.Unit == "allocs/op" && (len(d.Causes) != 2 || d.Causes[0].Side != "base" || d.Causes[1].Side != "new") {
			t.Fatalf("both-absent unit: %+v", d)
		}
	}
}

func TestPoliciesKeepNumericalRows(t *testing.T) {
	for _, fresh := range []string{"valid", "stale", "unverifiable", "unavailable", "not-applicable"} {
		for _, conditions := range []string{quietConds, "", "governor=unknown turbo=unknown throttled=unknown battery=unknown", strings.Replace(quietConds, "turbo=off", "turbo=on", 1), quietConds + " battery=", strings.Replace(quietConds, "load1=0.03", "load1=99", 1)} {
			for _, strictFresh := range []bool{false, true} {
				for _, strictConditions := range []bool{false, true} {
					o := DefaultOptions()
					o.Freshness = fresh
					if strictFresh {
						o.Policies.Freshness = "require"
					}
					if strictConditions {
						o.Policies.Conditions = "compatible"
					}
					b := condSet("BenchmarkX-8", quietConds, map[string][]float64{"sec/op": rep(1, 8)})
					n := condSet("BenchmarkX-8", conditions, map[string][]float64{"sec/op": rep(2, 8)})
					r := Compare(b, n, o)
					if r.ComparedRows() != 1 || !r.Tables[0].Rows[0].Regression {
						t.Fatalf("policy hid numeric regression: %+v", r)
					}
					compatible := conditions == quietConds || conditions == strings.Replace(quietConds, "load1=0.03", "load1=99", 1)
					want := (!strictFresh || fresh == "valid" || fresh == "not-applicable") && (!strictConditions || compatible)
					if got := r.Coverage(o.Policies); got.Satisfied != want || (got.Regressed == 1) != want {
						t.Fatalf("fresh=%s conditions=%q policy=%+v: %+v want eligibility %t", fresh, conditions, o.Policies, got, want)
					}
				}
			}
		}
	}
	// Matching unknowns are not evidence of compatibility.
	o := DefaultOptions()
	o.Policies.Conditions = "compatible"
	b := condSet("BenchmarkX-8", "governor=unknown turbo=unknown throttled=unknown battery=unknown", map[string][]float64{"sec/op": rep(1, 8)})
	if r := Compare(b, b, o); r.ComparedRows() != 1 || r.GatedComparisons() != 0 {
		t.Fatalf("unknowns admitted %+v", r)
	}
}

func TestInvalidSamplesRefuseWholeCell(t *testing.T) {
	for _, m := range metric.Definitions() {
		for _, bad := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), -1} {
			for _, side := range []string{"base", "new"} {
				o := DefaultOptions()
				o.GateUnits = map[string]bool{m.Unit: true}
				b := sampleSet("BenchmarkX-8", "m1", map[string][]float64{m.Unit: rep(1, 8), "widgets/op": rep(2, 8)})
				n := sampleSet("BenchmarkX-8", "m1", map[string][]float64{m.Unit: rep(2, 8), "widgets/op": rep(3, 8)})
				rows := b
				if side == "new" {
					rows = n
				}
				for i := range rows[0].Values {
					if rows[0].Values[i].Unit == m.Unit {
						rows[0].Values[i].Value = bad
					}
				}
				r := Compare(b, n, o)
				if r.ComparedRows() != 1 || r.GatedComparisons() != 0 || r.Regressed() {
					t.Fatalf("%s %s %v: %+v", m.Unit, side, bad, r)
				}
				found := false
				for _, d := range r.Dispositions {
					for _, c := range d.Causes {
						if c.Code == "invalid-sample" && c.Side == side {
							found = true
						}
					}
				}
				if !found {
					t.Fatal("invalid sample lost attribution")
				}
				if _, err := json.Marshal(r.Dispositions); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}

func TestFiniteCentersOverflowIsInvalidStatistic(t *testing.T) {
	// Unknown units can be signed. Interpolating across their finite extremes
	// overflows the subtraction inside x/perf's quantile computation.
	b := sampleSet("BenchmarkHuge-8", "m1", map[string][]float64{"widgets/op": alternate(-math.MaxFloat64, math.MaxFloat64, 4)})
	r := Compare(b, b, DefaultOptions())
	if r.ComparedRows() != 0 || r.GatedComparisons() != 0 {
		t.Fatalf("overflowed center entered evidence: %+v", r)
	}
	found := false
	for _, d := range r.Dispositions {
		for _, c := range d.Causes {
			if c.Code == "invalid-statistic" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("missing statistic cause: %+v", r.Dispositions)
	}
}

func TestStatisticalOutputAdmission(t *testing.T) {
	for _, p := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), -math.SmallestNonzeroFloat64, math.Nextafter(1, 2)} {
		if validStatistics(0, 1, p) {
			t.Fatalf("invalid probability admitted: %v", p)
		}
	}
	for _, center := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		if validStatistics(center, 1, 0.01) || validStatistics(1, center, 0.01) {
			t.Fatalf("invalid center admitted: %v", center)
		}
	}
	for _, p := range []float64{0, 0.5, 1} {
		if !validStatistics(0, math.MaxFloat64, p) {
			t.Fatalf("valid statistic refused: %v", p)
		}
	}
}

func TestCoverageSeparatesConfigurationsAndFullNames(t *testing.T) {
	var base, newer []*benchfmt.Result
	for _, name := range []string{"BenchmarkX/a-1", "BenchmarkX/a-8", "BenchmarkX/b-8"} {
		for _, cpu := range []string{"cpu-a", "cpu-b"} {
			r := condSet(name, quietConds, map[string][]float64{"sec/op": rep(1, 8)})
			for _, v := range r {
				v.Config = append(v.Config, benchfmt.Config{Key: "cpu", Value: []byte(cpu), File: true})
			}
			base = append(base, r...)
			if name != "BenchmarkX/b-8" || cpu != "cpu-b" {
				newer = append(newer, r...)
			}
		}
	}
	r := Compare(base, newer, DefaultOptions())
	c := r.Coverage(Policies{})
	if c.Requested != 6 || c.Eligible != 5 || c.Missing != 1 {
		t.Fatalf("collapsed identity: %+v", c)
	}
}

func TestMixedGuardDispositionIsPermutationInvariant(t *testing.T) {
	b := append(sampleSet("BenchmarkX-8", "m1", map[string][]float64{"sec/op": rep(1, 4)}), sampleSet("BenchmarkX-8", "m2", map[string][]float64{"sec/op": rep(1, 4)})...)
	n := sampleSet("BenchmarkX-8", "m1", map[string][]float64{"sec/op": rep(1, 8)})
	r := Compare(b, n, DefaultOptions())
	for i, j := 0, len(b)-1; i < j; i, j = i+1, j-1 {
		b[i], b[j] = b[j], b[i]
	}
	again := Compare(b, n, DefaultOptions())
	if !reflect.DeepEqual(r.Dispositions, again.Dispositions) || !reflect.DeepEqual(r.Notes, again.Notes) {
		t.Fatalf("mixed guard borrowed a sample's value: %+v versus %+v", r.Dispositions, again.Dispositions)
	}
	if r.GatedComparisons() != 0 || len(r.Dispositions) == 0 || r.Dispositions[0].Causes[0].Message != "mixed machine provenance" {
		t.Fatalf("mixed guard admitted: %+v", r)
	}
}

func FuzzCoverageConservation(f *testing.F) {
	for _, b := range [][]byte{{0}, {1, 2, 3}, {255, 8, 16, 32}, {7, 7, 7, 7, 7}, {128, 64, 32, 16}} {
		f.Add(b)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		// Every byte describes a complete identity; no prefix sampling or cap.
		var base, newer []*benchfmt.Result
		o := DefaultOptions()
		o.GateUnits = map[string]bool{"sec/op": true, "allocs/op": true}
		type identity struct{ name, cpu, unit string }
		type outcome struct{ compared, regressed bool }
		expected := map[identity]outcome{}
		wantCompared, wantRegressed := 0, 0
		for i, bits := range data {
			name := fmt.Sprintf("X/case%d-8", i)
			// The oracle comes from the input grammar, not the generated rows,
			// comparison grouping, returned counts, or registry iteration.
			presence := bits & 3
			if presence != 0 {
				cpus := []string{""}
				if bits&128 != 0 {
					cpus = nil
					if presence&1 != 0 {
						cpus = append(cpus, "a")
					}
					if presence&2 != 0 {
						cpus = append(cpus, "b")
					}
				}
				reported := "sec/op"
				if bits&4 != 0 {
					reported = "allocs/op"
				}
				valid := bits&64 != 0 || bits&(16|32) == 0
				for _, cpu := range cpus {
					for _, unit := range []string{"sec/op", "allocs/op"} {
						compared := presence == 3 && bits&128 == 0 && valid && unit == reported
						regressed := compared && bits&8 != 0 && bits&64 == 0
						expected[identity{name, cpu, unit}] = outcome{compared, regressed}
						if compared {
							wantCompared++
						}
						if regressed {
							wantRegressed++
						}
					}
				}
			}
			for side := 0; side < 2; side++ {
				if bits&(1<<side) == 0 {
					continue
				}
				unit := "sec/op"
				if bits&4 != 0 {
					unit = "allocs/op"
				}
				value := 1.0
				if bits&8 != 0 && side == 1 {
					value = 2
				}
				if bits&16 != 0 {
					value = math.NaN()
				}
				if bits&32 != 0 {
					value = -1
				}
				if bits&64 != 0 {
					value = 0
				}
				rs := condSet(name, quietConds, map[string][]float64{unit: rep(value, 8)})
				if bits&128 != 0 {
					for _, r := range rs {
						r.Config = append(r.Config, benchfmt.Config{Key: "cpu", Value: []byte(string(rune('a' + side))), File: true})
					}
				}
				if side == 0 {
					base = append(base, rs...)
				} else {
					newer = append(newer, rs...)
				}
			}
		}
		r := Compare(base, newer, o)
		c := r.Coverage(Policies{Coverage: "complete"})
		p := r.Coverage(Policies{})
		if c.Requested != len(expected) || c.Compared != wantCompared || c.Eligible != wantCompared || c.Regressed != wantRegressed {
			t.Fatalf("grammar universe: got %+v; want requested=%d compared=%d regressed=%d", c, len(expected), wantCompared, wantRegressed)
		}
		if c.Requested != c.Eligible+c.Missing || c.Eligible > c.Compared || c.Compared > c.Requested || (c.Satisfied && !p.Satisfied) {
			t.Fatalf("conservation: %+v %+v", c, p)
		}
		seen := map[string]bool{}
		for _, d := range r.Dispositions {
			cpu := ""
			for _, field := range strings.Fields(d.Config) {
				if field == "pkg:p" {
					continue
				}
				if value, ok := strings.CutPrefix(field, "cpu:"); ok {
					cpu = value
				} else {
					t.Fatalf("unexpected retained config: %q", d.Config)
				}
			}
			want, ok := expected[identity{d.Benchmark, cpu, d.Unit}]
			if !ok || !d.Requested || d.Compared != want.compared || d.Eligible != want.compared || d.Regression != want.regressed {
				t.Fatalf("unexpected disposition %+v; oracle %+v present=%t", d, want, ok)
			}
			key := d.Config + "\x00" + d.Benchmark + "\x00" + d.Unit
			if seen[key] {
				t.Fatal("duplicate obligation")
			}
			seen[key] = true
		}
		if len(seen) != len(expected) {
			t.Fatalf("lost universe members: got %d want %d", len(seen), len(expected))
		}
		for i, j := 0, len(base)-1; i < j; i, j = i+1, j-1 {
			base[i], base[j] = base[j], base[i]
		}
		for i, j := 0, len(newer)-1; i < j; i, j = i+1, j-1 {
			newer[i], newer[j] = newer[j], newer[i]
		}
		if again := Compare(base, newer, o); !reflect.DeepEqual(r.Dispositions, again.Dispositions) {
			t.Fatal("sample permutation changed coverage")
		}
	})
}
