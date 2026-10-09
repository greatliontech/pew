package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/guard"
	"github.com/greatliontech/gofresh/runtimeinput"
	runpkg "github.com/greatliontech/pew/internal/run"
)

// The explanation views (spec §12 --explain): a verdict or a skipped
// comparison surfaces one word, and the explanation lays the values it was
// decided over side by side. Environment inputs are disclosed as names with
// digest equality only — values never in clear text (§7.8).

// explainValue renders a recorded/current value for the table: digests are
// long and opaque, so they are elided to a recognizable prefix; equality is
// decided over the full values by the caller, never over the elision.
// Annotations (parenthesized diagnostics) are never elided — the text is the
// information — and a verbatim row (a version string) bypasses this
// function at the row.
func explainValue(v string) string {
	if v == "" {
		return "(absent)"
	}
	if len(v) > 24 && !strings.HasPrefix(v, "(") {
		return v[:24] + "…"
	}
	return v
}

// explainRow is one recorded/current pair; a verbatim row is a version
// string rendered whole — a derivation moves in a late component, and
// an elision would show two different derivations as one.
type explainRow struct {
	name, a, b string
	verbatim   bool
}

type explanationOutputError struct{ error }

func (e *explanationOutputError) Unwrap() error { return e.error }

func explanationWriteError(err error) error {
	if err == nil {
		return nil
	}
	return &explanationOutputError{err}
}

func writeExplainRows(w io.Writer, aLabel, bLabel string, rows []explainRow) error {
	if _, err := fmt.Fprintf(w, "    %-14s %-27s %-27s %s\n", "input", aLabel, bLabel, "match"); err != nil {
		return explanationWriteError(err)
	}
	for _, r := range rows {
		match := "yes"
		if r.a != r.b {
			match = "NO"
		}
		a, b := explainValue(r.a), explainValue(r.b)
		if r.verbatim {
			// Whole and quoted: the values carry spaces, so the quotes
			// are what marks where recorded ends and current begins.
			a, b = strconv.Quote(r.a), strconv.Quote(r.b)
		}
		if _, err := fmt.Fprintf(w, "    %-14s %-27s %-27s %s\n", r.name, a, b, match); err != nil {
			return explanationWriteError(err)
		}
	}
	return nil
}

func guardRows(a, b guard.Guards) []explainRow {
	return []explainRow{
		{name: runpkg.KeyToolchain.Display, a: a.Toolchain, b: b.Toolchain},
		{name: runpkg.KeyMachine.Display, a: a.Machine, b: b.Machine},
		{name: runpkg.KeyBuildConfig.Display, a: a.BuildConfig, b: b.BuildConfig},
		{name: runpkg.KeyRuntimeConfig.Display, a: a.RuntimeConfig, b: b.RuntimeConfig},
	}
}

func explainCapturedRecord(ctx context.Context, w io.Writer, moduleDir string, fp, curFP gofresh.Fingerprint, env []string) error {
	rows := guardRows(fp.Guards, curFP.Guards)
	rows = append(rows, explainRow{name: "dynamic-state strategy", a: fp.DynamicStateStrategy, b: curFP.DynamicStateStrategy, verbatim: true})
	rows = append(rows, explainRow{name: runpkg.KeyClosure.Display, a: fp.MaximalClosure, b: curFP.MaximalClosure})
	if fp.ClosureStrategy != curFP.ClosureStrategy {
		// A derivation move: the two closure hashes were folded by
		// different strategies and say nothing about each other's source.
		rows = append(rows, explainRow{name: runpkg.KeyClosureStrategy.Display, a: fp.ClosureStrategy, b: curFP.ClosureStrategy, verbatim: true})
	}
	rows = append(rows, explainRow{name: "producing test-variants", a: fp.TestVariantClosure, b: curFP.TestVariantClosure})
	rows = append(rows, explainRow{name: runpkg.KeyTestVariants.Display, a: fp.EffectiveTestVariantClosure(), b: curFP.TestVariantClosure})
	rows = append(rows, explainRow{name: "purity attribution", a: fp.PurityAssertion, b: curFP.PurityAssertion, verbatim: true})
	if _, err := fmt.Fprintf(w, "    recorded observation: assertion=%q strategy=%q observable=%t reason=%q; proof and outcome support are checked independently of purity\n", fp.ObservationAssertion, fp.ObservationProof.Strategy, fp.ObservationProof.Observable, fp.ObservationProof.Reason); err != nil {
		return explanationWriteError(err)
	}
	currentRuntime := ""
	var analysisErr error
	if fp.RuntimeInputs != "" {
		if _, err := fmt.Fprintln(w, "    runtime inputs: diagnostic re-observation of the recorded identities (values are not disclosed)"); err != nil {
			return explanationWriteError(err)
		}
		if st, err := runtimeinput.Current(ctx, fp.RuntimeInputs, moduleDir, env); err != nil {
			analysisErr = err
			rows = append(rows, explainRow{name: runpkg.KeyRuntime.Display, a: fp.RuntimeDigest, b: "(uncomputable: " + err.Error() + ")"})
		} else {
			currentRuntime = st.Digest
			rows = append(rows, explainRow{name: runpkg.KeyRuntime.Display, a: fp.RuntimeDigest, b: st.Digest})
		}
	}
	if err := writeExplainRows(w, "recorded", "current", rows); err != nil {
		return errors.Join(analysisErr, err)
	}
	if fp.RuntimeInputs == "" {
		return nil
	}
	d, err := runtimeinput.Describe(fp.RuntimeInputs, moduleDir)
	if err != nil {
		_, writeErr := fmt.Fprintf(w, "    watched inputs: undecodable manifest: %v\n", err)
		return errors.Join(analysisErr, err, explanationWriteError(writeErr))
	}
	if len(d.EnvNames) > 0 {
		if _, err := fmt.Fprintf(w, "    watched env (names only): %v\n", d.EnvNames); err != nil {
			return explanationWriteError(err)
		}
	}
	if len(d.Paths) > 0 {
		if _, err := fmt.Fprintf(w, "    watched paths: %v\n", d.Paths); err != nil {
			return explanationWriteError(err)
		}
	}
	if len(d.Unverifiable) > 0 {
		if _, err := fmt.Fprintf(w, "    unverifiable observations: %v\n", d.Unverifiable); err != nil {
			return explanationWriteError(err)
		}
	}
	// A moved digest names the moved inputs themselves (per-input digests
	// in the manifest), not only what was watched — env entries as names,
	// values never (§7.8).
	if currentRuntime != "" && currentRuntime != fp.RuntimeDigest {
		if line := movedInputsLine(ctx, fp.RuntimeInputs, moduleDir, env); line != "" {
			if _, err := fmt.Fprintf(w, "    moved inputs: %s\n", line); err != nil {
				return explanationWriteError(err)
			}
		}
	}
	return analysisErr
}

// movedInputsLine renders the moved-input attribution behind a runtime
// digest mismatch, best-effort: an attribution error or an empty result
// yields no line, and the digest row stays the whole story.
func movedInputsLine(ctx context.Context, manifest, moduleDir string, env []string) string {
	moved, err := runtimeinput.MovedInputs(ctx, manifest, moduleDir, env)
	if err != nil || len(moved) == 0 {
		return ""
	}
	return strings.Join(moved, ", ")
}
