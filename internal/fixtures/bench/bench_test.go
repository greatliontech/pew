package bench

import "testing"

//gofresh:pure
func BenchmarkDecode(b *testing.B) {
	for i := 0; i < b.N; i++ {
		Work()
	}
}
