package compare

import (
	"fmt"
	"testing"

	"golang.org/x/perf/benchfmt"
)

//gofresh:pure
func BenchmarkCompare(b *testing.B) {
	opts := DefaultOptions()
	var base, newer []*benchfmt.Result
	for i := range 8 {
		name := fmt.Sprintf("Work/case=%d-8", i)
		base = append(base, sampleSet(name, "m1", map[string][]float64{
			"sec/op":    seq(100e-9, 10),
			"B/op":      rep(256, 10),
			"allocs/op": rep(4, 10),
		})...)
		newer = append(newer, sampleSet(name, "m1", map[string][]float64{
			"sec/op":    seq(95e-9, 10),
			"B/op":      rep(256, 10),
			"allocs/op": rep(4, 10),
		})...)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		res := Compare(base, newer, opts)
		if res.ComparedRows() == 0 {
			b.Fatal("expected compared rows")
		}
	}
}
