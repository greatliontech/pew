//go:build !unix

package main

// sameDevice passes open where no portable device identity exists; the
// same-medium worktree contract (spec §12) is enforced on unix hosts.
func sameDevice(a, b string) (bool, error) {
	return true, nil
}
