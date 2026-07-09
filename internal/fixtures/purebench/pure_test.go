package purebench

import (
	"os"
	"testing"
)

// BenchmarkPureRead reaches file I/O (unverifiable closure) but carries the durable
// purity directive, so an engine that honors //gofresh:pure reports it valid.
//
//gofresh:pure
func BenchmarkPureRead(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_, _ = os.ReadFile("pure_test.go")
	}
}
