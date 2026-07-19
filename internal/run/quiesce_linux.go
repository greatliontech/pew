//go:build linux

package run

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

var (
	quiesceSysRoot     = "/sys"
	quiesceLoadavgPath = "/proc/loadavg"
)

// ObserveConditions reads the Linux sysfs/procfs signals for governor,
// AC/battery, load average, and turbo/boost into one Conditions snapshot
// (spec §9). The snapshot is both the quiesce-warning input and the recorded
// `pew-runconditions` provenance; a signal that cannot be read stays
// unobserved (recorded as the explicit unknown marker), never guessed.
// Throttling is deliberately absent here: it is run-scoped evidence, observed
// as a counter delta around each package's measurement (SnapshotThrottle) —
// a boot-cumulative counter says nothing about this run.
func ObserveConditions() Conditions {
	c := Conditions{
		Governor: observeGovernor(),
		Battery:  observeBattery(),
		Turbo:    observeTurbo(),
	}
	if la, ok := load1(); ok {
		c.Load1 = &la
	}
	return c
}

// observeGovernor reads every cpufreq policy's scaling governor: a box that is
// uniform records the value, differing policies record the explicit "mixed"
// marker (never policy0's value standing in for cores it doesn't govern), and
// no exposed policy falls back to the legacy per-cpu path. An exposed policy
// whose governor cannot be read leaves the whole signal unobserved: with one
// policy dark, neither uniformity nor a mix is provable.
func observeGovernor() string {
	matches, _ := filepath.Glob(sysPath("devices/system/cpu/cpufreq/policy*/scaling_governor"))
	if len(matches) == 0 {
		if g, err := os.ReadFile(sysPath("devices/system/cpu/cpu0/cpufreq/scaling_governor")); err == nil {
			return strings.TrimSpace(string(g))
		}
		return ""
	}
	governors := map[string]bool{}
	for _, m := range matches {
		b, err := os.ReadFile(m)
		if err != nil {
			return ""
		}
		g := strings.TrimSpace(string(b))
		if g == "" {
			return ""
		}
		governors[g] = true
	}
	if len(governors) == 1 {
		for g := range governors {
			return g
		}
	}
	return "mixed"
}

func sysPath(elem string) string {
	return filepath.Join(quiesceSysRoot, filepath.FromSlash(elem))
}

// observeBattery reports on-battery iff a Mains supply is offline. With Mains
// supplies exposed and all online it is an observed false; with none exposed
// (a desktop without power_supply class entries) it is unobserved, not "not on
// battery".
func observeBattery() *bool {
	var observed *bool
	for _, online := range globRead(sysPath("class/power_supply/*/online")) {
		dir := filepath.Dir(online.path)
		typ, _ := os.ReadFile(filepath.Join(dir, "type"))
		if strings.TrimSpace(string(typ)) != "Mains" {
			continue
		}
		switch strings.TrimSpace(online.data) {
		case "0":
			t := true
			return &t
		case "1":
			f := false
			observed = &f
		}
	}
	return observed
}

// observeTurbo reads the two sysfs turbo signals with driver precedence:
// `intel_pstate/no_turbo` belongs to the platform driver actually governing
// the CPU, so when it exposes a parseable value its verdict is final —
// `cpufreq/boost` is consulted only when intel_pstate says nothing. The old
// either-signal-on rule could report turbo enabled on a machine whose
// authoritative driver had it off.
func observeTurbo() *bool {
	if b, err := os.ReadFile(sysPath("devices/system/cpu/intel_pstate/no_turbo")); err == nil {
		switch strings.TrimSpace(string(b)) {
		case "0":
			t := true
			return &t
		case "1":
			f := false
			return &f
		}
	}
	if b, err := os.ReadFile(sysPath("devices/system/cpu/cpufreq/boost")); err == nil {
		switch strings.TrimSpace(string(b)) {
		case "1":
			t := true
			return &t
		case "0":
			f := false
			return &f
		}
	}
	return nil
}

// SnapshotThrottle reads every exposed CPU thermal-throttle counter. Two
// snapshots bracketing a measurement yield the run-scoped throttled verdict
// via Delta — the counters are boot-cumulative, so only an increase within
// the bracket says anything about the run.
func SnapshotThrottle() ThrottleSnapshot {
	var snap ThrottleSnapshot
	for _, f := range globRead(sysPath("devices/system/cpu/cpu*/thermal_throttle/*_throttle_count")) {
		count, err := strconv.ParseUint(strings.TrimSpace(f.data), 10, 64)
		if err != nil {
			continue
		}
		if snap == nil {
			snap = ThrottleSnapshot{}
		}
		snap[f.path] = count
	}
	return snap
}

type fileData struct{ path, data string }

func globRead(pattern string) []fileData {
	matches, _ := filepath.Glob(pattern)
	var out []fileData
	for _, m := range matches {
		if b, err := os.ReadFile(m); err == nil {
			out = append(out, fileData{m, string(b)})
		}
	}
	return out
}

func load1() (float64, bool) {
	b, err := os.ReadFile(quiesceLoadavgPath)
	if err != nil {
		return 0, false
	}
	fields := strings.Fields(string(b))
	if len(fields) == 0 {
		return 0, false
	}
	la, err := strconv.ParseFloat(fields[0], 64)
	return la, err == nil
}
