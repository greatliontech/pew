//go:build !linux

package run

// ObserveConditions has no signals on non-Linux platforms (pew targets Linux
// single-machine, §3): every condition stays unobserved, which records as
// explicit unknown fields (spec §9) and derives no quiesce warnings.
func ObserveConditions() Conditions { return Conditions{} }
