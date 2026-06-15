// Package compare implements pew's comparison & regression pipeline (spec §10).
//
// It is benchstat's own pipeline, imported in-process — never re-implementing the
// statistics (G4): benchfmt (parse, done by the caller) → benchproc (group by
// file config + benchmark name) → benchmath (median, confidence interval,
// Mann–Whitney U) → benchunit (value formatting). benchstat's comparison engine
// itself lives in an internal package, so this wires the same library slice it
// does rather than importing it.
//
// Grouping mirrors benchstat: results are grouped into tables by their file
// configuration (.config) and into rows by full benchmark name (.fullname). pew's
// own provenance keys (commit, toolchain, machine, buildconfig, dirty,
// pew-closure, pure) are projected away so that differing provenance between the
// two sides does not fragment the grouping (§10.1); the native keys go test emits
// (pkg, goos, goarch, cpu) are kept, so the same benchmark name in two different
// packages is never merged.
//
// A regression on a metric requires all three of (spec §10.1): the change is in
// the worse direction, it is statistically significant (p < α, Mann–Whitney U),
// and its magnitude clears a threshold. Comparisons are never made silently across
// machine fingerprints (§8, §10): a benchmark whose two sides were recorded on
// different machines is surfaced, not compared.
package compare

import (
	"fmt"
	"io"
	"math"
	"sort"
	"text/tabwriter"

	"golang.org/x/perf/benchfmt"
	"golang.org/x/perf/benchmath"
	"golang.org/x/perf/benchproc"
	"golang.org/x/perf/benchunit"
)

// pewIgnore are pew's own provenance keys, projected away from grouping so that
// the two sides (which legitimately differ in commit, closure, and possibly
// toolchain) still line up for comparison (§10.1). machine is ignored here too —
// it must not fragment grouping — but is enforced separately by the machine guard.
const pewIgnore = "commit toolchain machine buildconfig dirty pew-closure pure"

// Options configure the regression criterion (spec §10.1). Every field is a
// configurable default; the criterion itself is not a knob.
type Options struct {
	Alpha        float64         // significance level for Mann–Whitney U (default 0.05)
	ThresholdPct float64         // regression magnitude floor, in percent (default 3.0)
	Confidence   float64         // confidence level for summary intervals (default 0.95)
	GateUnits    map[string]bool // units whose regression fails the build (default {"sec/op"})
}

// DefaultOptions returns the spec's stated defaults (§9/§10.1).
func DefaultOptions() Options {
	return Options{
		Alpha:        0.05,
		ThresholdPct: 3.0,
		Confidence:   0.95,
		GateUnits:    map[string]bool{"sec/op": true},
	}
}

// Result is the outcome of a comparison: one table per (file config, unit) plus
// notes for benchmarks that could not be compared (one-sided, or a machine
// mismatch). The notes exist so that an un-comparable benchmark is never silently
// dropped.
type Result struct {
	Tables []*Table
	Notes  []string
}

// Table holds the per-benchmark comparison rows for one file configuration and
// one metric unit.
type Table struct {
	Config string // benchstat-style file config (pew keys excluded); "" if none
	Unit   string
	Rows   []Row
}

// Row is one benchmark's comparison for one unit.
type Row struct {
	Name     string
	Base     benchmath.Summary // median + CI of the baseline samples
	New      benchmath.Summary // median + CI of the new samples
	Cmp      benchmath.Comparison
	DeltaPct float64 // (new/base − 1)·100; NaN if the baseline center is 0
	// Regression is true iff the change is in the worse direction (higher
	// sec/op, B/op, allocs/op), statistically significant (p < α), and clears the
	// magnitude threshold — all three (spec §10.1).
	Regression bool
	// Gated is true iff this unit is in Options.GateUnits, i.e. a regression here
	// should drive a non-zero exit under --fail-on-regression (spec §10.1: sec/op
	// gates by default; B/op and allocs/op are flagged but opt-in to fail).
	Gated bool
	// Warnings carries benchmath's notes (e.g. too few samples for a finite CI),
	// surfaced rather than swallowed.
	Warnings []string
}

// Regressed reports whether any gated metric regressed — the condition that
// --fail-on-regression turns into a non-zero exit (spec §10.1).
func (r *Result) Regressed() bool {
	for _, t := range r.Tables {
		for _, row := range t.Rows {
			if row.Regression && row.Gated {
				return true
			}
		}
	}
	return false
}

// higherIsWorse is the set of units for which a larger value is a regression
// (spec §10.1: higher sec/op, B/op, allocs/op are worse). The direction is only
// defined for these; for any other unit (e.g. throughput "B/s", where higher is
// *better*) pew makes no regression judgement — the delta is shown but never
// flagged, the sound choice when the spec does not define the direction.
var higherIsWorse = map[string]bool{"sec/op": true, "B/op": true, "allocs/op": true}

type cell struct{ base, newer []float64 }

// group is one benchmark under one file configuration: its two sides' samples per
// unit, plus the per-side machine fingerprint for the machine guard. Machine and
// presence are tracked at the benchmark level (not per unit) so a mismatch or a
// one-sided benchmark yields a single note, not one per metric.
type group struct {
	name                string
	config              string
	hasBase, hasNew     bool
	baseMach, newMach   string
	baseMixed, newMixed bool     // a side carried >1 distinct non-empty machine
	units               []string // first-seen order, deduplicated
	cells               map[string]*cell
}

// recordMachine folds a result's machine fingerprint into a side. Empty (no
// machine info) is ignored; two distinct non-empty fingerprints on one side set
// mixed, so the guard refuses rather than silently picking one. In-spec each side
// of a group comes from a single overwrite-written file (one machine), so mixed
// only arises from hand-edited or externally-merged input — which the guard must
// still never compare across (§8, §10).
func recordMachine(cur string, mixed *bool, m string) string {
	if m == "" {
		return cur
	}
	if cur == "" {
		return m
	}
	if cur != m {
		*mixed = true
	}
	return cur
}

// Compare runs the regression pipeline over two already-parsed result sets.
func Compare(base, newer []*benchfmt.Result, opts Options) *Result {
	th := &benchmath.Thresholds{CompareAlpha: opts.Alpha}
	filter := mustFilter("*")
	var parser benchproc.ProjectionParser
	configBy := mustParse(&parser, ".config", filter)
	rowBy := mustParse(&parser, ".fullname", filter)
	mustParse(&parser, pewIgnore, filter) // excluded from .config grouping

	type gkey struct{ cfg, name benchproc.Key }
	groups := map[gkey]*group{}

	add := func(rs []*benchfmt.Result, isBase bool) {
		for _, r := range rs {
			gk := gkey{configBy.Project(r), rowBy.Project(r)}
			g := groups[gk]
			if g == nil {
				g = &group{name: string(r.Name), config: gk.cfg.String(), cells: map[string]*cell{}}
				groups[gk] = g
			}
			m := r.GetConfig("machine")
			if isBase {
				g.hasBase = true
				g.baseMach = recordMachine(g.baseMach, &g.baseMixed, m)
			} else {
				g.hasNew = true
				g.newMach = recordMachine(g.newMach, &g.newMixed, m)
			}
			for _, v := range r.Values {
				c := g.cells[v.Unit]
				if c == nil {
					c = &cell{}
					g.cells[v.Unit] = c
					g.units = append(g.units, v.Unit)
				}
				if isBase {
					c.base = append(c.base, v.Value)
				} else {
					c.newer = append(c.newer, v.Value)
				}
			}
		}
	}
	add(base, true)
	add(newer, false)

	// Deterministic order: by file config, then benchmark name.
	gs := make([]*group, 0, len(groups))
	for _, g := range groups {
		gs = append(gs, g)
	}
	sort.Slice(gs, func(i, j int) bool {
		if gs[i].config != gs[j].config {
			return gs[i].config < gs[j].config
		}
		return gs[i].name < gs[j].name
	})

	res := &Result{}
	type tkey struct{ config, unit string }
	tables := map[tkey]*Table{}
	for _, g := range gs {
		// One-sided: cannot compare. Surface so the omission is never silent.
		if !g.hasBase || !g.hasNew {
			side := "base"
			if !g.hasBase {
				side = "new"
			}
			res.Notes = append(res.Notes, fmt.Sprintf("%s: only present in %s; not compared", g.label(), side))
			continue
		}
		// Machine guard (spec §8, §10): never compare across machine fingerprints
		// silently. A side carrying mixed fingerprints, or two sides with differing
		// non-empty fingerprints, is surfaced and skipped. Equal fingerprints
		// (including both empty) compare.
		if g.baseMixed || g.newMixed {
			res.Notes = append(res.Notes, fmt.Sprintf("%s: mixed machine fingerprints within a side; not compared", g.label()))
			continue
		}
		if g.baseMach != g.newMach && g.baseMach != "" && g.newMach != "" {
			res.Notes = append(res.Notes, fmt.Sprintf("%s: machine mismatch (base=%s new=%s); not compared", g.label(), g.baseMach, g.newMach))
			continue
		}

		for _, unit := range sortUnits(g.units) {
			c := g.cells[unit]
			if len(c.base) == 0 || len(c.newer) == 0 {
				continue // metric present on only one side
			}
			bs := benchmath.NewSample(c.base, th)
			ns := benchmath.NewSample(c.newer, th)
			bsum := benchmath.AssumeNothing.Summary(bs, opts.Confidence)
			nsum := benchmath.AssumeNothing.Summary(ns, opts.Confidence)
			cmp := benchmath.AssumeNothing.Compare(bs, ns)

			delta := math.NaN()
			if bsum.Center != 0 {
				delta = (nsum.Center/bsum.Center - 1) * 100
			}
			// The three independent conditions of §10.1. Magnitude is on the
			// absolute change (|Δ| ≥ threshold); direction is a separate gate, so a
			// large *improvement* (|Δ| clears the floor but Δ < 0) is never a
			// regression. NaN delta (zero baseline) fails both comparisons.
			significant := cmp.P < opts.Alpha
			worse := higherIsWorse[unit] && delta > 0
			magnitude := math.Abs(delta) >= opts.ThresholdPct
			regression := significant && worse && magnitude

			tk := tkey{g.config, unit}
			t := tables[tk]
			if t == nil {
				t = &Table{Config: g.config, Unit: unit}
				tables[tk] = t
			}
			t.Rows = append(t.Rows, Row{
				Name:       g.name,
				Base:       bsum,
				New:        nsum,
				Cmp:        cmp,
				DeltaPct:   delta,
				Regression: regression,
				Gated:      opts.GateUnits[unit],
				Warnings:   collectWarnings(bsum.Warnings, nsum.Warnings, cmp.Warnings),
			})
		}
	}

	res.Tables = make([]*Table, 0, len(tables))
	for _, t := range tables {
		res.Tables = append(res.Tables, t)
	}
	sort.Slice(res.Tables, func(i, j int) bool {
		if res.Tables[i].Config != res.Tables[j].Config {
			return res.Tables[i].Config < res.Tables[j].Config
		}
		ri, rj := unitRank(res.Tables[i].Unit), unitRank(res.Tables[j].Unit)
		if ri != rj {
			return ri < rj
		}
		return res.Tables[i].Unit < res.Tables[j].Unit
	})
	return res
}

// label names a benchmark in a note, qualifying it with its config when present
// so cross-config notes are unambiguous.
func (g *group) label() string {
	if g.config == "" {
		return g.name
	}
	return g.name + " [" + g.config + "]"
}

// unitOrder is the preferred display order; unknown units follow alphabetically.
var unitOrder = []string{"sec/op", "B/op", "allocs/op"}

func unitRank(u string) int {
	for i, known := range unitOrder {
		if u == known {
			return i
		}
	}
	return len(unitOrder)
}

func sortUnits(units []string) []string {
	out := append([]string(nil), units...)
	sort.Slice(out, func(i, j int) bool {
		ri, rj := unitRank(out[i]), unitRank(out[j])
		if ri != rj {
			return ri < rj
		}
		return out[i] < out[j]
	})
	return out
}

// collectWarnings flattens benchmath's []error warning lists into deduplicated
// strings (the same "need ≥ N samples" message can come from both sides).
func collectWarnings(lists ...[]error) []string {
	var out []string
	seen := map[string]bool{}
	for _, list := range lists {
		for _, e := range list {
			s := e.Error()
			if !seen[s] {
				seen[s] = true
				out = append(out, s)
			}
		}
	}
	return out
}

// mustFilter / mustParse build benchproc objects from compile-time-constant
// expressions known valid, so an error is unreachable here (cf. regexp.MustCompile);
// a future non-constant caller would panic at first use, surfacing the bug
// immediately rather than miscomparing.
func mustFilter(expr string) *benchproc.Filter {
	f, err := benchproc.NewFilter(expr)
	if err != nil {
		panic("compare: filter " + expr + ": " + err.Error())
	}
	return f
}

func mustParse(parser *benchproc.ProjectionParser, expr string, filter *benchproc.Filter) *benchproc.Projection {
	proj, err := parser.Parse(expr, filter)
	if err != nil {
		panic("compare: projection " + expr + ": " + err.Error())
	}
	return proj
}

// WriteText renders r as a benchstat-style table to w — a section per file config,
// a sub-table per unit — and marks each regressing metric with "⚠ regression"
// (spec §10.1). Benchmarks that could not be compared are listed as notes after
// the tables.
func (r *Result) WriteText(w io.Writer) error {
	prevConfig := "\x00" // sentinel distinct from "" (the no-config case)
	for i, t := range r.Tables {
		if i > 0 {
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
		if t.Config != prevConfig {
			if t.Config != "" {
				if _, err := fmt.Fprintln(w, t.Config); err != nil {
					return err
				}
			}
			prevConfig = t.Config
		}
		if _, err := fmt.Fprintln(w, t.Unit); err != nil {
			return err
		}
		cls := benchunit.ClassOf(t.Unit)
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "benchmark\tbase\tnew\tvs base")
		for _, row := range t.Rows {
			base := benchunit.Scale(row.Base.Center, cls) + " ± " + row.Base.PctRangeString()
			newv := benchunit.Scale(row.New.Center, cls) + " ± " + row.New.PctRangeString()
			delta := row.Cmp.FormatDelta(row.Base.Center, row.New.Center) + " (" + row.Cmp.String() + ")"
			if row.Regression {
				delta += "  ⚠ regression"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", row.Name, base, newv, delta)
		}
		if err := tw.Flush(); err != nil {
			return err
		}
		for _, row := range t.Rows {
			for _, warn := range row.Warnings {
				if _, err := fmt.Fprintf(w, "  %s: %s\n", row.Name, warn); err != nil {
					return err
				}
			}
		}
	}
	if len(r.Notes) > 0 {
		if len(r.Tables) > 0 {
			if _, err := fmt.Fprintln(w); err != nil {
				return err
			}
		}
		for _, n := range r.Notes {
			if _, err := fmt.Fprintln(w, "note:", n); err != nil {
				return err
			}
		}
	}
	return nil
}
