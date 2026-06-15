//go:build linux

package run

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// Quiesce returns advisory warnings about conditions that make benchmarks noisy
// (spec §9). It checks the CPU governor, AC/battery, and load average. (Turbo and
// thermal-throttle checks are not yet implemented — a documented subset.)
func Quiesce() []string {
	var warns []string
	if g, err := os.ReadFile("/sys/devices/system/cpu/cpu0/cpufreq/scaling_governor"); err == nil {
		if gov := strings.TrimSpace(string(g)); gov != "" && gov != "performance" {
			warns = append(warns, "cpu governor is "+gov+", not performance")
		}
	}
	if onBattery() {
		warns = append(warns, "running on battery")
	}
	if la, ok := load1(); ok && la > float64(runtime.NumCPU())*0.3 {
		warns = append(warns, "high load average ("+strconv.FormatFloat(la, 'f', 2, 64)+")")
	}
	return warns
}

func onBattery() bool {
	// Any Mains supply that is offline ⇒ on battery.
	for _, online := range globRead("/sys/class/power_supply/*/online") {
		dir := filepath.Dir(online.path)
		typ, _ := os.ReadFile(filepath.Join(dir, "type"))
		if strings.TrimSpace(string(typ)) == "Mains" && strings.TrimSpace(online.data) == "0" {
			return true
		}
	}
	return false
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
	b, err := os.ReadFile("/proc/loadavg")
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
