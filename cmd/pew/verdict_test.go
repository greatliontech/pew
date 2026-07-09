package main

import (
	"testing"

	gofresh "github.com/greatliontech/gofresh"
	"golang.org/x/perf/benchfmt"
)

// TestApplyPurity pins the recorded purity-flag semantics (spec §7.3, §7.5):
// --impure re-runs on any non-stale verdict; --assume-pure lifts unverifiable to
// valid after every hashable guard held; a stale verdict is never overridden.
func TestApplyPurity(t *testing.T) {
	staleV := gofresh.Verdict{Status: gofresh.Stale, Reason: "closure"}
	validV := gofresh.Verdict{Status: gofresh.Valid}
	unverV := gofresh.Verdict{Status: gofresh.Unverifiable, Reason: "reaches os.Open"}

	tests := []struct {
		name string
		in   gofresh.Verdict
		pure string
		want gofresh.Verdict
	}{
		{"no flag passes through valid", validV, "", validV},
		{"no flag passes through unverifiable", unverV, "", unverV},
		{"impure demotes valid", validV, "false", gofresh.Verdict{Status: gofresh.Unverifiable, Reason: "impure"}},
		{"impure demotes unverifiable", unverV, "false", gofresh.Verdict{Status: gofresh.Unverifiable, Reason: "impure"}},
		{"impure never overrides stale", staleV, "false", staleV},
		{"assume-pure lifts unverifiable", unverV, "true", validV},
		{"assume-pure never lifts stale", staleV, "true", staleV},
		{"assume-pure leaves valid alone", validV, "true", validV},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := applyPurity(tc.in, tc.pure); got != tc.want {
				t.Errorf("applyPurity(%+v, %q) = %+v, want %+v", tc.in, tc.pure, got, tc.want)
			}
		})
	}
}

// TestFingerprintFromConfig pins the config-line ↔ fingerprint mapping (spec §5:
// pew owns the serialization; gofresh owns the semantics).
func TestFingerprintFromConfig(t *testing.T) {
	cfg := []benchfmt.Config{
		{Key: "commit", Value: []byte("c1")},
		{Key: "toolchain", Value: []byte("tc")},
		{Key: "machine", Value: []byte("m")},
		{Key: "buildconfig", Value: []byte("bc")},
		{Key: "runtimeconfig", Value: []byte("rc")},
		{Key: "pew-closure", Value: []byte("cl")},
		{Key: "pew-runtime", Value: []byte("rd")},
		{Key: "pew-runtime-inputs", Value: []byte("manifest")},
		{Key: "pure", Value: []byte("true")},
	}
	fp, pure := fingerprintFromConfig(cfg)
	if fp.Closure != "cl" || fp.RuntimeInputs != "manifest" || fp.RuntimeDigest != "rd" {
		t.Errorf("fingerprint closure/runtime fields = %+v", fp)
	}
	g := fp.Guards
	if g.Toolchain != "tc" || g.BuildConfig != "bc" || g.Machine != "m" || g.RuntimeConfig != "rc" {
		t.Errorf("guards = %+v", g)
	}
	if pure != "true" {
		t.Errorf("pure = %q, want true", pure)
	}

	fp, pure = fingerprintFromConfig(nil)
	if fp != (gofresh.Fingerprint{}) || pure != "" {
		t.Errorf("empty config: fp=%+v pure=%q, want zero values", fp, pure)
	}
}
