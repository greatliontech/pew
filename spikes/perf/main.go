// Spike 3: validate x/perf benchfmt round-trips our in-band provenance config
// lines, and that benchmath is importable for stats.
package main

import (
	"fmt"
	"strings"

	"golang.org/x/perf/benchfmt"
	"golang.org/x/perf/benchmath"
)

const data = `goos: linux
goarch: amd64
pkg: example.com/pewspikes/sample
cpu: TestCPU
commit: 79d7e29deadbeef
commit-time: 2026-06-15T10:00:00Z
machine: m-deadbeef
toolchain: go1.26.4
buildconfig: default
BenchmarkRun-8 1000000 1234 ns/op 456 B/op 7 allocs/op
BenchmarkRun-8 1000000 1250 ns/op 456 B/op 7 allocs/op
BenchmarkRun-8 1000000 1228 ns/op 456 B/op 7 allocs/op
`

func main() {
	r := benchfmt.NewReader(strings.NewReader(data), "inline")
	var nsop []float64
	for r.Scan() {
		switch rec := r.Result().(type) {
		case *benchfmt.Result:
			fmt.Printf("name=%s iters=%d\n", string(rec.Name), rec.Iters)
			for _, c := range rec.Config {
				fmt.Printf("  config %s = %s\n", c.Key, string(c.Value))
			}
			for _, v := range rec.Values {
				fmt.Printf("  value  %g %s\n", v.Value, v.Unit)
				if v.Unit == "sec/op" { // benchfmt normalizes ns/op -> sec/op
					nsop = append(nsop, v.Value)
				}
			}
		case *benchfmt.SyntaxError:
			fmt.Println("syntax error:", rec)
		}
	}
	if err := r.Err(); err != nil {
		fmt.Println("read err:", err)
	}

	fmt.Printf("\ncollected %d ns/op samples: %v\n", len(nsop), nsop)
	if len(nsop) > 0 {
		s := benchmath.NewSample(nsop, &benchmath.DefaultThresholds)
		sum := benchmath.AssumeNothing.Summary(s, 0.95)
		fmt.Printf("benchmath summary: center=%.2f  CI=[%.2f, %.2f]\n", sum.Center, sum.Lo, sum.Hi)
	}
}
