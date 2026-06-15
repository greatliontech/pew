// Package stale computes the staleness verdict for a stored benchmark recording
// against the current code/toolchain/machine/build state (spec §7).
package stale

import (
	"strings"

	"github.com/thegrumpylion/pew/internal/gotool"
	"github.com/thegrumpylion/pew/internal/provenance"
	"golang.org/x/perf/benchfmt"
)

// Verdict is a recording's status relative to HEAD (spec §7).
type Verdict string

const (
	Valid        Verdict = "valid"
	Stale        Verdict = "stale"
	Unverifiable Verdict = "unverifiable" // produced once Class-B detection lands (chunk 7)
	Unrecorded   Verdict = "unrecorded"
)

// Check returns the validity verdict for a recording (its config lines) against
// current state — the conjunction of the four guards (§7, INV-2): closure,
// toolchain, machine, buildconfig. commit/dirty are deliberately NOT guards
// (INV-6: validity is commit-sha-independent). A recording with no config is
// Unrecorded; a missing guard key cannot be validated, so it is Stale. For a
// Stale verdict, reason names the first failing guard.
func Check(cur provenance.Provenance, curClosure string, recorded []benchfmt.Config) (Verdict, string) {
	// The guards rely on current values being non-empty (curClosure is a hash;
	// toolchain/machine/buildconfig come from capture, which hard-errors on an
	// empty machine identity). An empty current value matching an empty recorded
	// value would false-valid — upstream capture guarantees non-empty.
	if len(recorded) == 0 {
		return Unrecorded, ""
	}
	cfg := configMap(recorded)
	for _, g := range []struct{ key, want string }{
		{"pew-closure", curClosure},
		{"toolchain", cur.Toolchain},
		{"machine", cur.Machine},
		{"buildconfig", cur.BuildConfig},
	} {
		if got, ok := cfg[g.key]; !ok || got != g.want {
			return Stale, g.key
		}
	}
	return Valid, ""
}

func configMap(cfg []benchfmt.Config) map[string]string {
	m := make(map[string]string, len(cfg))
	for _, c := range cfg {
		m[c.Key] = string(c.Value)
	}
	return m
}

// ListBenchmarks returns the top-level benchmark function names in pkg's test
// binary (via `go test -list`), without running them.
func ListBenchmarks(pkg string) ([]string, error) {
	out, err := gotool.Run("test", "-list", "^Benchmark", pkg)
	if err != nil {
		return nil, err
	}
	return parseBenchList(out), nil
}

func parseBenchList(out []byte) []string {
	var names []string
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		if line = strings.TrimSpace(line); strings.HasPrefix(line, "Benchmark") {
			names = append(names, line)
		}
	}
	return names
}
