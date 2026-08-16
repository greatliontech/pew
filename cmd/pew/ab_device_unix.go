//go:build unix

package main

import (
	"os"
	"syscall"
)

// sameDevice reports whether two paths sit on one filesystem device — the
// enforcement behind ab's same-medium worktree contract (spec §12). A stat
// whose platform representation carries no device id passes open.
func sameDevice(a, b string) (bool, error) {
	ia, err := os.Stat(a)
	if err != nil {
		return false, err
	}
	ib, err := os.Stat(b)
	if err != nil {
		return false, err
	}
	sa, okA := ia.Sys().(*syscall.Stat_t)
	sb, okB := ib.Sys().(*syscall.Stat_t)
	if !okA || !okB {
		return true, nil
	}
	return sa.Dev == sb.Dev, nil
}
