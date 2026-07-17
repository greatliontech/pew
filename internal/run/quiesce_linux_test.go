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

func TestQuiesceWarnsForTurboAndThermalSignals(t *testing.T) {
	sysRoot, loadavg := withQuiesceFS(t)
	writeQuiesceFile(t, filepath.Join(sysRoot, "devices/system/cpu/cpu0/cpufreq/scaling_governor"), "performance\n")
	writeQuiesceFile(t, filepath.Join(sysRoot, "class/power_supply/AC/type"), "Mains\n")
	writeQuiesceFile(t, filepath.Join(sysRoot, "class/power_supply/AC/online"), "1\n")
	writeQuiesceFile(t, filepath.Join(sysRoot, "devices/system/cpu/intel_pstate/no_turbo"), "0\n")
	writeQuiesceFile(t, filepath.Join(sysRoot, "devices/system/cpu/cpu0/thermal_throttle/core_throttle_count"), "2\n")
	writeQuiesceFile(t, loadavg, "0.00 0.00 0.00 1/1 1\n")

	warns := ObserveConditions().Warnings()
	if !contains(warns, "cpu turbo/boost is enabled") {
		t.Fatalf("warnings = %v, want turbo warning", warns)
	}
	if !contains(warns, "thermal throttling observed") {
		t.Fatalf("warnings = %v, want thermal warning", warns)
	}
}

func TestQuiesceWarnsForCpufreqBoost(t *testing.T) {
	sysRoot, loadavg := withQuiesceFS(t)
	writeQuiesceFile(t, filepath.Join(sysRoot, "devices/system/cpu/intel_pstate/no_turbo"), "1\n")
	writeQuiesceFile(t, filepath.Join(sysRoot, "devices/system/cpu/cpufreq/boost"), "1\n")
	writeQuiesceFile(t, loadavg, "0.00 0.00 0.00 1/1 1\n")

	warns := ObserveConditions().Warnings()
	if !contains(warns, "cpu turbo/boost is enabled") {
		t.Fatalf("warnings = %v, want boost warning", warns)
	}
}

func TestQuiesceNoWarningForQuietTurboAndThermalSignals(t *testing.T) {
	sysRoot, loadavg := withQuiesceFS(t)
	writeQuiesceFile(t, filepath.Join(sysRoot, "devices/system/cpu/cpu0/cpufreq/scaling_governor"), "performance\n")
	writeQuiesceFile(t, filepath.Join(sysRoot, "class/power_supply/AC/type"), "Mains\n")
	writeQuiesceFile(t, filepath.Join(sysRoot, "class/power_supply/AC/online"), "1\n")
	writeQuiesceFile(t, filepath.Join(sysRoot, "devices/system/cpu/intel_pstate/no_turbo"), "1\n")
	writeQuiesceFile(t, filepath.Join(sysRoot, "devices/system/cpu/cpufreq/boost"), "0\n")
	writeQuiesceFile(t, filepath.Join(sysRoot, "devices/system/cpu/cpu0/thermal_throttle/core_throttle_count"), "0\n")
	writeQuiesceFile(t, loadavg, "0.00 0.00 0.00 1/1 1\n")

	if warns := ObserveConditions().Warnings(); len(warns) != 0 {
		t.Fatalf("warnings = %v, want none", warns)
	}
}

// TestObserveConditionsReadsSysfsSignals pins the observed → recorded mapping
// (spec §9): a fully-exposed quiet system reads as fully-observed values.
func TestObserveConditionsReadsSysfsSignals(t *testing.T) {
	sysRoot, loadavg := withQuiesceFS(t)
	writeQuiesceFile(t, filepath.Join(sysRoot, "devices/system/cpu/cpu0/cpufreq/scaling_governor"), "performance\n")
	writeQuiesceFile(t, filepath.Join(sysRoot, "class/power_supply/AC/type"), "Mains\n")
	writeQuiesceFile(t, filepath.Join(sysRoot, "class/power_supply/AC/online"), "1\n")
	writeQuiesceFile(t, filepath.Join(sysRoot, "devices/system/cpu/intel_pstate/no_turbo"), "1\n")
	writeQuiesceFile(t, filepath.Join(sysRoot, "devices/system/cpu/cpu0/thermal_throttle/core_throttle_count"), "0\n")
	writeQuiesceFile(t, loadavg, "0.03 0.10 0.05 1/1 1\n")

	c := ObserveConditions()
	if got, want := c.String(), "governor=performance turbo=off load1=0.03 throttled=false battery=false"; got != want {
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

// TestObserveConditionsRecordsOnBatteryAndThrottled pins the noisy direction of
// the tri-state signals.
func TestObserveConditionsRecordsOnBatteryAndThrottled(t *testing.T) {
	sysRoot, loadavg := withQuiesceFS(t)
	writeQuiesceFile(t, filepath.Join(sysRoot, "devices/system/cpu/cpu0/cpufreq/scaling_governor"), "powersave\n")
	writeQuiesceFile(t, filepath.Join(sysRoot, "class/power_supply/AC/type"), "Mains\n")
	writeQuiesceFile(t, filepath.Join(sysRoot, "class/power_supply/AC/online"), "0\n")
	writeQuiesceFile(t, filepath.Join(sysRoot, "devices/system/cpu/intel_pstate/no_turbo"), "0\n")
	writeQuiesceFile(t, filepath.Join(sysRoot, "devices/system/cpu/cpu0/thermal_throttle/core_throttle_count"), "3\n")
	writeQuiesceFile(t, loadavg, "7.50 5.00 4.00 3/300 12\n")

	c := ObserveConditions()
	if got, want := c.String(), "governor=powersave turbo=on load1=7.50 throttled=true battery=true"; got != want {
		t.Fatalf("conditions = %q, want %q", got, want)
	}
	warns := c.Warnings()
	for _, want := range []string{
		"cpu governor is powersave, not performance",
		"running on battery",
		"cpu turbo/boost is enabled",
		"thermal throttling observed",
	} {
		if !contains(warns, want) {
			t.Errorf("warnings = %v, missing %q", warns, want)
		}
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
