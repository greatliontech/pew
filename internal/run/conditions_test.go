package run

import (
	"reflect"
	"testing"
)

func boolPtr(v bool) *bool        { return &v }
func floatPtr(v float64) *float64 { return &v }

// TestConditionsStringExplicitUnknowns pins the recorded line shape (spec §9):
// fixed field order, every field always present, unobserved signals rendered as
// the explicit unknown marker — the zero Conditions is exactly the non-Linux
// producer's line.
func TestConditionsStringExplicitUnknowns(t *testing.T) {
	if got, want := (Conditions{}).String(), "governor=unknown turbo=unknown load1=unknown throttled=unknown battery=unknown"; got != want {
		t.Fatalf("zero conditions = %q, want %q", got, want)
	}
	full := Conditions{
		Governor:  "performance",
		Turbo:     boolPtr(false),
		Load1:     floatPtr(0.031),
		Throttled: boolPtr(false),
		Battery:   boolPtr(true),
	}
	if got, want := full.String(), "governor=performance turbo=off load1=0.03 throttled=false battery=true"; got != want {
		t.Fatalf("observed conditions = %q, want %q", got, want)
	}
}

// TestConditionsStringSanitizesGovernorToken: a governor value that is not a
// plain token would corrupt the line's key=value structure, so it records as
// unknown (spec §9).
func TestConditionsStringSanitizesGovernorToken(t *testing.T) {
	for _, bad := range []string{"weird gov", "a=b", "x\ny", " "} {
		c := Conditions{Governor: bad}
		if got, want := c.String(), "governor=unknown turbo=unknown load1=unknown throttled=unknown battery=unknown"; got != want {
			t.Errorf("Governor %q rendered %q, want sanitized unknown line", bad, got)
		}
	}
	c := Conditions{Governor: "sched_util-2.0"}
	if got, want := c.String(), "governor=sched_util-2.0 turbo=unknown load1=unknown throttled=unknown battery=unknown"; got != want {
		t.Errorf("token governor rendered %q, want %q", got, want)
	}
}

// TestConditionsWarningsOnlyObservedNoisySignals: unknown signals never warn
// (the zero Conditions warns nothing — the non-Linux behavior), observed quiet
// signals never warn, observed noisy ones each warn. Throttled never warns
// here: it is run-scoped evidence warned after the measurement (spec §9), not
// a pre-run signal.
func TestConditionsWarningsOnlyObservedNoisySignals(t *testing.T) {
	if warns := (Conditions{}).Warnings(); len(warns) != 0 {
		t.Fatalf("zero conditions warned: %v", warns)
	}
	quiet := Conditions{Governor: "performance", Turbo: boolPtr(false), Load1: floatPtr(0), Throttled: boolPtr(false), Battery: boolPtr(false)}
	if warns := quiet.Warnings(); len(warns) != 0 {
		t.Fatalf("quiet conditions warned: %v", warns)
	}
	noisy := Conditions{Governor: "powersave", Turbo: boolPtr(true), Load1: floatPtr(10000), Throttled: boolPtr(true), Battery: boolPtr(true)}
	want := []string{
		"cpu governor is powersave, not performance",
		"running on battery",
		"high load average (10000.00)",
		"cpu turbo/boost is enabled",
	}
	if got := noisy.Warnings(); !reflect.DeepEqual(got, want) {
		t.Fatalf("noisy warnings = %v, want %v", got, want)
	}
}
