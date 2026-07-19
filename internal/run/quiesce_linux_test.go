//go:build linux

package run

import (
	"os"
	"path/filepath"
	"testing"
)

func withQuiesceFS(t *testing.T) (string, string) {
	t.Helper()
	base := t.TempDir()
	sysRoot := filepath.Join(base, "sys")
	loadavg := filepath.Join(base, "proc", "loadavg")
	oldSysRoot := quiesceSysRoot
	oldLoadavg := quiesceLoadavgPath
	quiesceSysRoot = sysRoot
	quiesceLoadavgPath = loadavg
	t.Cleanup(func() {
		quiesceSysRoot = oldSysRoot
		quiesceLoadavgPath = oldLoadavg
	})
	return sysRoot, loadavg
}

func writeQuiesceFile(t *testing.T, path, data string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestQuiesceWarnsForTurboSignal(t *testing.T) {
	sysRoot, loadavg := withQuiesceFS(t)
	writeQuiesceFile(t, filepath.Join(sysRoot, "devices/system/cpu/cpu0/cpufreq/scaling_governor"), "performance\n")
	writeQuiesceFile(t, filepath.Join(sysRoot, "class/power_supply/AC/type"), "Mains\n")
	writeQuiesceFile(t, filepath.Join(sysRoot, "class/power_supply/AC/online"), "1\n")
	writeQuiesceFile(t, filepath.Join(sysRoot, "devices/system/cpu/intel_pstate/no_turbo"), "0\n")
	writeQuiesceFile(t, loadavg, "0.00 0.00 0.00 1/1 1\n")

	warns := ObserveConditions().Warnings()
	if !contains(warns, "cpu turbo/boost is enabled") {
		t.Fatalf("warnings = %v, want turbo warning", warns)
	}
}

// TestQuiesceTurboDriverPrecedence pins spec §9's turbo precedence: an exposed
// parseable intel_pstate/no_turbo is authoritative and cpufreq/boost is
// consulted only in its absence — conflicting signals never resolve toward
// enabled (the pre-fix rule reported turbo on for no_turbo=1 + boost=1).
func TestQuiesceTurboDriverPrecedence(t *testing.T) {
	cases := map[string]struct {
		noTurbo, boost string // "" = file absent
		want           string // on / off / unknown
	}{
		"no_turbo off wins over boost on": {noTurbo: "1", boost: "1", want: "off"},
		"no_turbo on wins over boost off": {noTurbo: "0", boost: "0", want: "on"},
		"boost consulted without pstate":  {noTurbo: "", boost: "1", want: "on"},
		"boost off without pstate":        {noTurbo: "", boost: "0", want: "off"},
		"neither exposed":                 {noTurbo: "", boost: "", want: "unknown"},
		"unparseable pstate falls back":   {noTurbo: "x", boost: "1", want: "on"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			sysRoot, _ := withQuiesceFS(t)
			if tc.noTurbo != "" {
				writeQuiesceFile(t, filepath.Join(sysRoot, "devices/system/cpu/intel_pstate/no_turbo"), tc.noTurbo+"\n")
			}
			if tc.boost != "" {
				writeQuiesceFile(t, filepath.Join(sysRoot, "devices/system/cpu/cpufreq/boost"), tc.boost+"\n")
			}
			c := ObserveConditions()
			got := "unknown"
			if c.Turbo != nil {
				got = "off"
				if *c.Turbo {
					got = "on"
				}
			}
			if got != tc.want {
				t.Fatalf("turbo = %s, want %s", got, tc.want)
			}
		})
	}
}

// TestQuiesceGovernorScansAllPolicies pins spec §9's governor observation: a
// uniform box records the value, differing policies record the explicit mixed
// marker (which warns — mixed is not performance), and the legacy per-cpu
// path is only a fallback when no policy is exposed.
func TestQuiesceGovernorScansAllPolicies(t *testing.T) {
	t.Run("mixed policies", func(t *testing.T) {
		sysRoot, _ := withQuiesceFS(t)
		writeQuiesceFile(t, filepath.Join(sysRoot, "devices/system/cpu/cpufreq/policy0/scaling_governor"), "performance\n")
		writeQuiesceFile(t, filepath.Join(sysRoot, "devices/system/cpu/cpufreq/policy1/scaling_governor"), "powersave\n")
		// The legacy path mirrors policy0 — it must not mask the mix.
		writeQuiesceFile(t, filepath.Join(sysRoot, "devices/system/cpu/cpu0/cpufreq/scaling_governor"), "performance\n")
		c := ObserveConditions()
		if c.Governor != "mixed" {
			t.Fatalf("governor = %q, want mixed", c.Governor)
		}
		if warns := c.Warnings(); !contains(warns, "cpu governor is mixed, not performance") {
			t.Fatalf("warnings = %v, want the mixed-governor warning", warns)
		}
	})
	t.Run("uniform policies", func(t *testing.T) {
		sysRoot, _ := withQuiesceFS(t)
		writeQuiesceFile(t, filepath.Join(sysRoot, "devices/system/cpu/cpufreq/policy0/scaling_governor"), "performance\n")
		writeQuiesceFile(t, filepath.Join(sysRoot, "devices/system/cpu/cpufreq/policy4/scaling_governor"), "performance\n")
		if c := ObserveConditions(); c.Governor != "performance" {
			t.Fatalf("governor = %q, want performance", c.Governor)
		}
	})
	t.Run("legacy fallback", func(t *testing.T) {
		sysRoot, _ := withQuiesceFS(t)
		writeQuiesceFile(t, filepath.Join(sysRoot, "devices/system/cpu/cpu0/cpufreq/scaling_governor"), "schedutil\n")
		if c := ObserveConditions(); c.Governor != "schedutil" {
			t.Fatalf("governor = %q, want schedutil via the legacy path", c.Governor)
		}
	})
	t.Run("dark policy makes the signal unobserved", func(t *testing.T) {
		if os.Geteuid() == 0 {
			t.Skip("root reads through file modes")
		}
		sysRoot, _ := withQuiesceFS(t)
		writeQuiesceFile(t, filepath.Join(sysRoot, "devices/system/cpu/cpufreq/policy0/scaling_governor"), "performance\n")
		dark := filepath.Join(sysRoot, "devices/system/cpu/cpufreq/policy1/scaling_governor")
		writeQuiesceFile(t, dark, "powersave\n")
		if err := os.Chmod(dark, 0o200); err != nil {
			t.Fatal(err)
		}
		if c := ObserveConditions(); c.Governor != "" {
			t.Fatalf("governor = %q, want unobserved with an unreadable exposed policy", c.Governor)
		}
	})
	t.Run("blank policy makes the signal unobserved", func(t *testing.T) {
		sysRoot, _ := withQuiesceFS(t)
		writeQuiesceFile(t, filepath.Join(sysRoot, "devices/system/cpu/cpufreq/policy0/scaling_governor"), "performance\n")
		writeQuiesceFile(t, filepath.Join(sysRoot, "devices/system/cpu/cpufreq/policy1/scaling_governor"), "\n")
		if c := ObserveConditions(); c.Governor != "" {
			t.Fatalf("governor = %q, want unobserved with a blank exposed policy", c.Governor)
		}
	})
}

// TestThrottleSnapshotDelta pins spec §9's run-scoped throttling verdict: a
// counter moving between the bracketing snapshots is true, comparable still
// counters are false, and nothing comparable is unobserved.
func TestThrottleSnapshotDelta(t *testing.T) {
	sysRoot, _ := withQuiesceFS(t)
	counter := filepath.Join(sysRoot, "devices/system/cpu/cpu0/thermal_throttle/core_throttle_count")
	writeQuiesceFile(t, counter, "5\n")
	base := SnapshotThrottle()
	if base == nil {
		t.Fatal("exposed counter not snapshotted")
	}

	if d := base.Delta(SnapshotThrottle()); d == nil || *d {
		t.Fatalf("still counters delta = %v, want observed false", d)
	}
	writeQuiesceFile(t, counter, "7\n")
	if d := base.Delta(SnapshotThrottle()); d == nil || !*d {
		t.Fatalf("moved counter delta = %v, want observed true", d)
	}
	// Boot-cumulative standing value alone must never read as throttled: a
	// fresh bracket over the higher-but-still counter is quiet again.
	base = SnapshotThrottle()
	if d := base.Delta(SnapshotThrottle()); d == nil || *d {
		t.Fatalf("standing non-zero counter delta = %v, want observed false", d)
	}

	var none ThrottleSnapshot
	if d := none.Delta(none); d != nil {
		t.Fatalf("no counters delta = %v, want unobserved", d)
	}
}

func TestQuiesceNoWarningForQuietSignals(t *testing.T) {
	sysRoot, loadavg := withQuiesceFS(t)
	writeQuiesceFile(t, filepath.Join(sysRoot, "devices/system/cpu/cpu0/cpufreq/scaling_governor"), "performance\n")
	writeQuiesceFile(t, filepath.Join(sysRoot, "class/power_supply/AC/type"), "Mains\n")
	writeQuiesceFile(t, filepath.Join(sysRoot, "class/power_supply/AC/online"), "1\n")
	writeQuiesceFile(t, filepath.Join(sysRoot, "devices/system/cpu/intel_pstate/no_turbo"), "1\n")
	writeQuiesceFile(t, filepath.Join(sysRoot, "devices/system/cpu/cpufreq/boost"), "0\n")
	writeQuiesceFile(t, loadavg, "0.00 0.00 0.00 1/1 1\n")

	if warns := ObserveConditions().Warnings(); len(warns) != 0 {
		t.Fatalf("warnings = %v, want none", warns)
	}
}

// TestObserveConditionsReadsSysfsSignals pins the observed → recorded mapping
// (spec §9): a fully-exposed quiet system reads as fully-observed values.
// Throttled stays unknown here by design — it is run-scoped (a measurement
// bracket delta), never part of the pre-run observation.
func TestObserveConditionsReadsSysfsSignals(t *testing.T) {
	sysRoot, loadavg := withQuiesceFS(t)
	writeQuiesceFile(t, filepath.Join(sysRoot, "devices/system/cpu/cpu0/cpufreq/scaling_governor"), "performance\n")
	writeQuiesceFile(t, filepath.Join(sysRoot, "class/power_supply/AC/type"), "Mains\n")
	writeQuiesceFile(t, filepath.Join(sysRoot, "class/power_supply/AC/online"), "1\n")
	writeQuiesceFile(t, filepath.Join(sysRoot, "devices/system/cpu/intel_pstate/no_turbo"), "1\n")
	writeQuiesceFile(t, filepath.Join(sysRoot, "devices/system/cpu/cpu0/thermal_throttle/core_throttle_count"), "0\n")
	writeQuiesceFile(t, loadavg, "0.03 0.10 0.05 1/1 1\n")

	c := ObserveConditions()
	if got, want := c.String(), "governor=performance turbo=off load1=0.03 throttled=unknown battery=false"; got != want {
		t.Fatalf("conditions = %q, want %q", got, want)
	}
}

// TestObserveConditionsLeavesUnexposedSignalsUnknown: signals whose sysfs files
// are absent stay unobserved and record as explicit unknown fields, never as a
// guessed quiet value (spec §9 fail-closed posture).
func TestObserveConditionsLeavesUnexposedSignalsUnknown(t *testing.T) {
	withQuiesceFS(t)

	c := ObserveConditions()
	if got, want := c.String(), "governor=unknown turbo=unknown load1=unknown throttled=unknown battery=unknown"; got != want {
		t.Fatalf("conditions = %q, want %q", got, want)
	}
	if warns := c.Warnings(); len(warns) != 0 {
		t.Fatalf("unobserved conditions warned: %v", warns)
	}
}

// TestObserveConditionsRecordsNoisySignals pins the noisy direction of the
// pre-run signals. The standing non-zero throttle counter deliberately does
// NOT read as throttled: boot-cumulative counts never leak into a run's
// provenance (spec §9) — only a measurement-bracket delta sets the field.
func TestObserveConditionsRecordsNoisySignals(t *testing.T) {
	sysRoot, loadavg := withQuiesceFS(t)
	writeQuiesceFile(t, filepath.Join(sysRoot, "devices/system/cpu/cpu0/cpufreq/scaling_governor"), "powersave\n")
	writeQuiesceFile(t, filepath.Join(sysRoot, "class/power_supply/AC/type"), "Mains\n")
	writeQuiesceFile(t, filepath.Join(sysRoot, "class/power_supply/AC/online"), "0\n")
	writeQuiesceFile(t, filepath.Join(sysRoot, "devices/system/cpu/intel_pstate/no_turbo"), "0\n")
	writeQuiesceFile(t, filepath.Join(sysRoot, "devices/system/cpu/cpu0/thermal_throttle/core_throttle_count"), "3\n")
	writeQuiesceFile(t, loadavg, "7.50 5.00 4.00 3/300 12\n")

	c := ObserveConditions()
	if got, want := c.String(), "governor=powersave turbo=on load1=7.50 throttled=unknown battery=true"; got != want {
		t.Fatalf("conditions = %q, want %q", got, want)
	}
	warns := c.Warnings()
	for _, want := range []string{
		"cpu governor is powersave, not performance",
		"running on battery",
		"cpu turbo/boost is enabled",
	} {
		if !contains(warns, want) {
			t.Errorf("warnings = %v, missing %q", warns, want)
		}
	}
	if contains(warns, "thermal throttling observed") {
		t.Errorf("warnings = %v: boot-cumulative counter warned pre-run", warns)
	}
}

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
