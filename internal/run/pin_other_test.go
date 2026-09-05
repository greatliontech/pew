//go:build !linux

package run

import (
	"strings"
	"testing"
)

// Off Linux there is no topology to derive a pin from: the request
// refuses naming the platform, never a guessed set (REQ-pew-pin-derivation).
func TestDerivePinRefusesOffLinux(t *testing.T) {
	pin, err := DerivePin()
	if err == nil || !strings.Contains(err.Error(), "not available on") || len(pin.CPUs) != 0 {
		t.Fatalf("DerivePin = %+v, %v; want the platform refusal", pin, err)
	}
}
