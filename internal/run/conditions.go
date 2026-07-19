package run

import (
	"regexp"
	"runtime"
	"strconv"
)

// Conditions is one pre-run observation of the transient run conditions
// (spec §9): the signals the quiesce warnings evaluate, recorded in-band as the
// `pew-runconditions` provenance line. A nil pointer field (or empty Governor)
// means the signal was not observable; it renders as the explicit field value
// "unknown" — an unobserved condition is stated, never implied. Conditions are
// provenance only, never identity or validity (spec §5.1, INV-9).
//
// One observation feeds both the warnings and the recorded line, so the
// recording states exactly the conditions the warn/--strict gate evaluated —
// there is no second read that could diverge from the first.
type Conditions struct {
	Governor  string   // cpufreq scaling governor, "mixed" on differing policies; "" = not observed
	Turbo     *bool    // cpu turbo/boost enabled; nil = not observed
	Load1     *float64 // 1-minute load average; nil = not observed
	Throttled *bool    // thermal throttling during the measurement (counter delta); nil = not observed
	Battery   *bool    // running on battery power; nil = not observed
}

// ThrottleSnapshot maps a thermal-throttle counter file to its cumulative
// count. The counters count events since boot, so a single snapshot carries no
// run information; two snapshots bracketing a measurement do.
type ThrottleSnapshot map[string]uint64

// Delta reports whether any counter increased between the base snapshot and
// now: true when one did (the run was throttled), false when at least one
// counter is present on both sides and none moved, nil when nothing is
// comparable (unobserved, never guessed quiet).
func (s ThrottleSnapshot) Delta(now ThrottleSnapshot) *bool {
	var observed *bool
	for path, base := range s {
		cur, ok := now[path]
		if !ok {
			continue
		}
		if cur > base {
			t := true
			return &t
		}
		quiet := false
		observed = &quiet
	}
	return observed
}

const conditionUnknown = "unknown"

// governorTokenRe is the value shape a recorded governor must have (spec §9):
// field values never carry separators that could corrupt the line's `key=value`
// structure, so a non-token governor read from sysfs is recorded as unknown.
var governorTokenRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)

// String renders the observation as the `pew-runconditions` value (spec §9):
// fixed field order, every field present, unknowns explicit.
func (c Conditions) String() string {
	governor := conditionUnknown
	if c.Governor != "" && governorTokenRe.MatchString(c.Governor) {
		governor = c.Governor
	}
	turbo := conditionUnknown
	if c.Turbo != nil {
		turbo = "off"
		if *c.Turbo {
			turbo = "on"
		}
	}
	load1 := conditionUnknown
	if c.Load1 != nil {
		load1 = strconv.FormatFloat(*c.Load1, 'f', 2, 64)
	}
	return "governor=" + governor +
		" turbo=" + turbo +
		" load1=" + load1 +
		" throttled=" + conditionBool(c.Throttled) +
		" battery=" + conditionBool(c.Battery)
}

func conditionBool(v *bool) string {
	if v == nil {
		return conditionUnknown
	}
	return strconv.FormatBool(*v)
}

// Warnings derives the advisory pre-run quiesce warnings (spec §9) from the
// observation. Only observed noisy signals warn: an unknown signal is neither
// quiet nor noisy, so it produces no warning (and a platform with no signals —
// the zero Conditions — produces none at all). Throttling has no pre-run
// warning: it is run-scoped evidence (a counter delta across the measurement,
// spec §9), warned where it is observed — after the package's run.
func (c Conditions) Warnings() []string {
	var warns []string
	if c.Governor != "" && c.Governor != "performance" {
		warns = append(warns, "cpu governor is "+c.Governor+", not performance")
	}
	if c.Battery != nil && *c.Battery {
		warns = append(warns, "running on battery")
	}
	if c.Load1 != nil && *c.Load1 > float64(runtime.NumCPU())*0.3 {
		warns = append(warns, "high load average ("+strconv.FormatFloat(*c.Load1, 'f', 2, 64)+")")
	}
	if c.Turbo != nil && *c.Turbo {
		warns = append(warns, "cpu turbo/boost is enabled")
	}
	return warns
}
