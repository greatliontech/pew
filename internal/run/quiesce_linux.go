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
// AC/battery, load average, turbo/boost, and thermal throttling into one
// Conditions snapshot (spec §9). The snapshot is both the quiesce-warning input
// and the recorded `pew-runconditions` provenance; a signal that cannot be read
// stays unobserved (recorded as the explicit unknown marker), never guessed.
func ObserveConditions() Conditions {
	c := Conditions{}
	if g, err := os.ReadFile(sysPath("devices/system/cpu/cpu0/cpufreq/scaling_governor")); err == nil {
		c.Governor = strings.TrimSpace(string(g))
	}
	c.Battery = observeBattery()
	if la, ok := load1(); ok {
		c.Load1 = &la
	}
	c.Turbo = observeTurbo()
	c.Throttled = observeThrottled()
	return c
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

// observeTurbo reads the two sysfs turbo signals. Turbo counts as enabled when
// either exposed signal says so (`intel_pstate/no_turbo` == 0 or
// `cpufreq/boost` == 1); as disabled when at least one is exposed and neither
// says enabled; and as unobserved when neither is exposed with a parseable
// value.
func observeTurbo() *bool {
	var observed *bool
	if b, err := os.ReadFile(sysPath("devices/system/cpu/intel_pstate/no_turbo")); err == nil {
		switch strings.TrimSpace(string(b)) {
		case "0":
			t := true
			return &t
		case "1":
			f := false
			observed = &f
		}
	}
	if b, err := os.ReadFile(sysPath("devices/system/cpu/cpufreq/boost")); err == nil {
		switch strings.TrimSpace(string(b)) {
		case "1":
			t := true
			return &t
		case "0":
			f := false
			observed = &f
		}
	}
	return observed
}

// observeThrottled reports whether any exposed CPU thermal-throttle counter is
// non-zero; with no parseable counter exposed the signal is unobserved.
func observeThrottled() *bool {
	var observed *bool
	for _, f := range globRead(sysPath("devices/system/cpu/cpu*/thermal_throttle/*_throttle_count")) {
		count, err := strconv.ParseUint(strings.TrimSpace(f.data), 10, 64)
		if err != nil {
			continue
		}
		if count > 0 {
			t := true
			return &t
		}
		quiet := false
		observed = &quiet
	}
	return observed
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
