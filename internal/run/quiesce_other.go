//go:build !linux

package run

// Quiesce has no checks on non-Linux platforms yet (pew targets Linux
// single-machine, §3).
func Quiesce() []string { return nil }
