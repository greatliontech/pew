//go:build !linux

package run

// ObserveConditions has no signals on non-Linux platforms (pew targets Linux
// single-machine, §3): every condition stays unobserved, which records as
// explicit unknown fields (spec §9) and derives no quiesce warnings.
func ObserveConditions() Conditions { return Conditions{} }

// SnapshotThrottle has no counters on non-Linux platforms: the throttled
// field stays unobserved (nil Delta), recorded as the explicit unknown.
func SnapshotThrottle() ThrottleSnapshot { return nil }
