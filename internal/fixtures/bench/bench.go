// Package bench is a test fixture: a package with one benchmark, giving cmd tests
// a real closure subject to compute against.
package bench

// Work is the benchmarked function.
func Work() int {
	s := 0
	for i := 0; i < 100; i++ {
		s += i
	}
	return s
}
