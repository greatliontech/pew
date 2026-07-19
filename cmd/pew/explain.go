package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/guard"
	"github.com/greatliontech/gofresh/runtimeinput"
	"golang.org/x/perf/benchfmt"
)

// The explanation views (spec §12 --explain): a verdict or a skipped
// comparison surfaces one word, and the explanation lays the values it was
// decided over side by side. Environment inputs are disclosed as names with
// digest equality only — values never in clear text (§7.8).

// explainValue renders a recorded/current value for the table: digests are
// long and opaque, so they are elided to a recognizable prefix; equality is
// decided over the full values by the caller, never over the elision.
// Annotations (parenthesized diagnostics) are never elided — the text is the
// information.
func explainValue(v string) string {
	if v == "" {
		return "(absent)"
	}
	if len(v) > 24 && !strings.HasPrefix(v, "(") {
		return v[:24] + "…"
	}
	return v
}

type explainRow struct{ name, a, b string }

func writeExplainRows(w io.Writer, aLabel, bLabel string, rows []explainRow) {
	fmt.Fprintf(w, "    %-14s %-27s %-27s %s\n", "input", aLabel, bLabel, "match")
	for _, r := range rows {
		match := "yes"
		if r.a != r.b {
			match = "NO"
		}
		fmt.Fprintf(w, "    %-14s %-27s %-27s %s\n", r.name, explainValue(r.a), explainValue(r.b), match)
	}
}

func guardRows(a, b guard.Guards) []explainRow {
	return []explainRow{
		{"toolchain", a.Toolchain, b.Toolchain},
		{"machine", a.Machine, b.Machine},
		{"buildconfig", a.BuildConfig, b.BuildConfig},
		{"runtimeconfig", a.RuntimeConfig, b.RuntimeConfig},
	}
}

// explainRecordAgainstCurrent explains one recording against the current tree
// and environment: every guard's recorded vs current value, the closure hash,
// the runtime-input digest, and — because a digest mismatch alone names
// nothing — the manifest's watched identities. Current values come from the
// engine's own capture, so they are digested exactly as the recorded ones were
// (the engine folds build inputs with its own framing; a parallel capture
// would diverge under PGO).
func explainRecordAgainstCurrent(w io.Writer, e *gofresh.Engine, moduleDir, importPath, bench string, fp gofresh.Fingerprint, env []string) {
	ctx := context.Background()
	curFP, err := e.CaptureFor(ctx, gofresh.Subject{Package: importPath, Symbol: bench}, moduleDir, gofresh.Measurement)
	if err != nil {
		fmt.Fprintf(w, "    cannot compute the current state: %v\n", err)
		return
	}
	rows := guardRows(fp.Guards, curFP.Guards)
	rows = append(rows, explainRow{"closure", fp.MaximalClosure, curFP.MaximalClosure})
	if fp.RuntimeInputs != "" {
		if st, err := runtimeinput.CurrentEnv(fp.RuntimeInputs, moduleDir, env); err != nil {
			rows = append(rows, explainRow{"runtime", fp.RuntimeDigest, "(uncomputable: " + err.Error() + ")"})
		} else {
			rows = append(rows, explainRow{"runtime", fp.RuntimeDigest, st.Digest})
		}
	}
	writeExplainRows(w, "recorded", "current", rows)
	if fp.RuntimeInputs == "" {
		return
	}
	d, err := runtimeinput.Describe(fp.RuntimeInputs, moduleDir)
	if err != nil {
		fmt.Fprintf(w, "    watched inputs: undecodable manifest: %v\n", err)
		return
	}
	if len(d.EnvNames) > 0 {
		fmt.Fprintf(w, "    watched env (names only): %v\n", d.EnvNames)
	}
	if len(d.Paths) > 0 {
		fmt.Fprintf(w, "    watched paths: %v\n", d.Paths)
	}
	if len(d.Unverifiable) > 0 {
		fmt.Fprintf(w, "    unverifiable observations: %v\n", d.Unverifiable)
	}
}

// explainSides explains a skipped comparison between two recordings: the
// recorded guard values side by side, so a guard-mismatch skip names the
// moving guard instead of one word.
func explainSides(w io.Writer, aLabel, bLabel string, base, new []*benchfmt.Result) {
	a, aOK := recordedGuards(base)
	b, bOK := recordedGuards(new)
	if !aOK || !bOK {
		return
	}
	writeExplainRows(w, aLabel, bLabel, guardRows(a, b))
}

func recordedGuards(recs []*benchfmt.Result) (guard.Guards, bool) {
	if len(recs) == 0 {
		return guard.Guards{}, false
	}
	fp, _, ok := fingerprintFromConfig(recs[0].Config)
	return fp.Guards, ok
}
