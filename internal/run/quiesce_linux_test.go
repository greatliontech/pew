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

	warns := Quiesce()
	if !contains(warns, "cpu turbo/boost is enabled") {
		t.Fatalf("Quiesce warnings = %v, want turbo warning", warns)
	}
	if !contains(warns, "thermal throttling observed") {
		t.Fatalf("Quiesce warnings = %v, want thermal warning", warns)
	}
}

func TestQuiesceWarnsForCpufreqBoost(t *testing.T) {
	sysRoot, loadavg := withQuiesceFS(t)
	writeQuiesceFile(t, filepath.Join(sysRoot, "devices/system/cpu/intel_pstate/no_turbo"), "1\n")
	writeQuiesceFile(t, filepath.Join(sysRoot, "devices/system/cpu/cpufreq/boost"), "1\n")
	writeQuiesceFile(t, loadavg, "0.00 0.00 0.00 1/1 1\n")

	warns := Quiesce()
	if !contains(warns, "cpu turbo/boost is enabled") {
		t.Fatalf("Quiesce warnings = %v, want boost warning", warns)
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

	if warns := Quiesce(); len(warns) != 0 {
		t.Fatalf("Quiesce warnings = %v, want none", warns)
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
