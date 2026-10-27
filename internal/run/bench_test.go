package run

import (
	"strings"
	"testing"
)

//gofresh:pure
func BenchmarkParseStream(b *testing.B) {
	var sb strings.Builder
	sb.WriteString("goos: linux\ngoarch: amd64\npkg: example.com/pkg\ncpu: BenchmarkCPU\n")
	for range 10 {
		sb.WriteString("BenchmarkAlpha-8\t1000000\t1050 ns/op\t256 B/op\t4 allocs/op\n")
		sb.WriteString("BenchmarkBeta/sub=1-8\t500000\t2100 ns/op\t512 B/op\t8 allocs/op\n")
	}
	sb.WriteString("PASS\n")
	stream := []byte(sb.String())
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		results, corrupt, _, err := Parse(stream)
		if err != nil || len(corrupt) != 0 || len(results) != 20 {
			b.Fatalf("Parse = (%d, %d, %v)", len(results), len(corrupt), err)
		}
	}
}
