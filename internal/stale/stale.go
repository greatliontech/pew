// Package stale computes the staleness verdict for a stored benchmark recording
// against the current code/runtime-input/toolchain/machine/build state (spec §7).
package stale

import (
	"github.com/thegrumpylion/pew/internal/closure"
	"github.com/thegrumpylion/pew/internal/provenance"
	"golang.org/x/perf/benchfmt"
)

// Verdict is a recording's status relative to HEAD (spec §7).
type Verdict string

const (
	Valid        Verdict = "valid"
	Stale        Verdict = "stale"
	Unverifiable Verdict = "unverifiable"
	Unrecorded   Verdict = "unrecorded"
)

// RuntimeState is the recomputed current state of a recording's runtime-input
// manifest (§7.8). OK=false means the manifest was missing or malformed, so the
// runtime guard cannot be proven and is stale.
type RuntimeState struct {
	Digest       string
	Unverifiable bool
	Reason       string
	OK           bool
}

// Check returns the validity verdict for a recording (its config lines) against
// current state (§7, INV-2):
//
//   - Unrecorded if there is no config.
//   - Stale if any guard fails: closure, runtime inputs, toolchain, machine,
//     buildconfig, runtimeconfig.
//     commit/dirty are deliberately NOT guards (INV-6: validity is
//     commit-sha-independent). A missing guard key cannot be validated, so Stale.
//   - Unverifiable if the guards pass and the runtime manifest contains an
//     unhashable observed input, the benchmark is marked --impure (pure:false),
//     or its HEAD closure reaches an unhashable external dependence
//     (head.Unverifiable) and it is not marked --assume-pure (pure:true).
//     Absence of proof never collapses to valid (INV-1).
//
// For a Stale verdict, reason names the first failing guard; for Unverifiable, it
// is the external-dependence reason (or "impure").
func Check(cur provenance.Provenance, head closure.Closure, runtime RuntimeState, recorded []benchfmt.Config) (Verdict, string) {
	// The guards rely on current values being non-empty (head.Hash is a hash;
	// toolchain/machine/buildconfig come from capture, which hard-errors on an
	// empty machine identity). An empty current value matching an empty recorded
	// value would false-valid — upstream capture guarantees non-empty.
	if len(recorded) == 0 {
		return Unrecorded, ""
	}
	cfg := configMap(recorded)
	if got, ok := cfg["pew-closure"]; !ok || got != head.Hash {
		return Stale, "pew-closure"
	}
	if _, ok := cfg["pew-runtime-inputs"]; !ok || !runtime.OK {
		return Stale, "pew-runtime"
	}
	if got, ok := cfg["pew-runtime"]; !ok || got != runtime.Digest {
		return Stale, "pew-runtime"
	}
	for _, g := range []struct{ key, want string }{
		{"toolchain", cur.Toolchain},
		{"machine", cur.Machine},
		{"buildconfig", cur.BuildConfig},
		{"runtimeconfig", cur.RuntimeConfig},
	} {
		if got, ok := cfg[g.key]; !ok || got != g.want {
			return Stale, g.key
		}
	}
	if runtime.Unverifiable {
		reason := runtime.Reason
		if reason == "" {
			reason = "runtime inputs"
		}
		return Unverifiable, reason
	}
	switch cfg["pure"] {
	case "false":
		// --impure: the author declares external state, so it always re-runs (§7.3).
		return Unverifiable, "impure"
	case "true":
		// --assume-pure: Class-B detection is suppressed (§7.5); fall through to the
		// guards as if verifiable.
	default:
		// No purity directive: an unhashable external dependence in HEAD's closure
		// makes validity unprovable (§7.3 Class B). The runtime-input guard catches
		// changes to inputs it observed, but it is not itself proof that every
		// reachable file-I/O path was observed (§7.8).
		if head.Unverifiable {
			reason := head.Reason
			if reason == "" {
				reason = "external dependence"
			}
			return Unverifiable, reason
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
