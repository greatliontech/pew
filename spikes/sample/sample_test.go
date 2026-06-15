package sample

import "testing"

func BenchmarkRun(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = Run("world", i%2 == 0)
	}
}

func BenchmarkReflect(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = UsesReflect(i)
	}
}

func BenchmarkSort(b *testing.B) {
	for i := 0; i < b.N; i++ {
		SortByLen([]string{"ccc", "a", "bb"})
	}
}
