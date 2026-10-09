// Package metric defines the measurement domains and regression directions shared
// by recording admission and comparison. Unknown units remain informational.
package metric

import "math"

// Definition is one canonical benchmark metric's domain and comparison policy.
type Definition struct {
	Unit          string
	HigherIsWorse bool
	Nonnegative   bool
	DefaultGate   bool
}

// Definitions returns metrics in display order, independently owned by the caller.
func Definitions() []Definition {
	return []Definition{{"sec/op", true, true, true}, {"B/op", true, true, false}, {"allocs/op", true, true, false}}
}

// Lookup returns a known canonical metric.
func Lookup(unit string) (Definition, bool) {
	for _, d := range Definitions() {
		if d.Unit == unit {
			return d, true
		}
	}
	return Definition{}, false
}

// Finite reports whether v is an ordinary representable number.
func Finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// ValidSample applies the finite domain to all metrics and the nonnegative
// domain to known costs. It never guesses the direction of an unknown unit.
func ValidSample(unit string, v float64) bool {
	d, _ := Lookup(unit)
	return Finite(v) && (!d.Nonnegative || v >= 0)
}

// Rank returns the canonical display rank; unknown units sort afterwards.
func Rank(unit string) int {
	for i, d := range Definitions() {
		if d.Unit == unit {
			return i
		}
	}
	return len(Definitions())
}
