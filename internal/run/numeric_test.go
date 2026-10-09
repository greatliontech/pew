package run

import (
	"math"
	"testing"

	"github.com/greatliontech/pew/internal/metric"
	"golang.org/x/perf/benchfmt"
)

func TestAuditStreamRefusesInvalidMetricsPerBenchmark(t *testing.T) {
	for _, definition := range metric.Definitions() {
		for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), -1} {
			rows := []*benchfmt.Result{
				{Name: []byte("Bad/sub-8"), Values: []benchfmt.Value{{Unit: definition.Unit, Value: value}}},
				{Name: []byte("Good-8"), Values: []benchfmt.Value{{Unit: definition.Unit, Value: 1}}},
			}
			a := AuditStream(rows, nil, 1, []string{"BenchmarkBad", "BenchmarkGood"})
			if len(a.Refused["BenchmarkBad"]) == 0 || len(a.Refused["BenchmarkGood"]) != 0 || a.PackageCause != "" {
				t.Fatalf("%s %v: %+v", definition.Unit, value, a)
			}
		}
	}
}

func FuzzMetricStreamAdmission(f *testing.F) {
	for _, text := range []string{"BenchmarkBad-8 1 NaN ns/op\n", "BenchmarkBad-8 1 +Inf B/op\n", "BenchmarkBad-8 1 -1 allocs/op\n", "BenchmarkBad/sub-8 1 0 ns/op\n", "BenchmarkBad-8 1 -2 custom/op\n"} {
		f.Add(text)
	}
	f.Fuzz(func(t *testing.T, text string) {
		rows, corrupt, _, err := Parse([]byte(text))
		if err != nil {
			return
		}
		a := AuditStream(rows, corrupt, 1, []string{"BenchmarkBad"})
		for _, r := range rows {
			if BenchName(string(r.Name)) == "BenchmarkBad" {
				for _, v := range r.Values {
					if !metric.ValidSample(v.Unit, v.Value) && len(a.Refused["BenchmarkBad"]) == 0 {
						t.Fatalf("invalid sample admitted: %q", text)
					}
				}
			}
		}
	})
}
