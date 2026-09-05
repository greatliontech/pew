//go:build !linux

package run

import (
	"fmt"
	"runtime"
)

// DerivePin has no topology to read off Linux: a pin request refuses
// with the reason rather than guessing a CPU set (spec §9).
func DerivePin() (Pin, error) {
	return Pin{}, fmt.Errorf("CPU pinning derives its set from Linux sysfs; not available on %s", runtime.GOOS)
}
