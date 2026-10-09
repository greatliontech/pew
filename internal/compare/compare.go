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
// pew-runconditions, pew-closure, pew-runtime, pew-runtime-inputs) are
// projected away so that differing provenance between the two sides does not
// fragment the grouping (§10.1); the native keys go test emits
// (pkg, goos, goarch, cpu) are kept, so the same benchmark name in two different
// packages is never merged.
//
// A regression on a metric requires all three of (spec §10.1): the change is in
// the worse direction, it is statistically significant (p < α, Mann–Whitney U),
// and its magnitude clears a threshold. Comparisons are never made silently across
// provenance guards that define a side-by-side variant (§6, §8, §10): a benchmark
// whose two sides differ in machine, toolchain, or buildconfig is surfaced, not
// compared.
package compare

import (
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/greatliontech/pew/internal/metric"
	"github.com/greatliontech/pew/internal/run"
	"golang.org/x/perf/benchfmt"
	"golang.org/x/perf/benchmath"
	"golang.org/x/perf/benchproc"
	"golang.org/x/perf/benchunit"
)

// pewIgnore are pew's own provenance keys, projected away from grouping so that
// the two sides (which legitimately differ in commit, closure, runtime-input
// digest, and dirty flag) still line up for comparison (§10.1). Variant guards
// are ignored here too so they do not fragment grouping, but are enforced
// separately by compareGuards. Derived from the producer's single
// registry, so a new recorded key can never fragment grouping by
// omission here.
var pewIgnore = func() string {
	names := append([]string{}, run.RecordingConfigKeys...)
	for _, k := range run.FingerprintProjectionKeys {
		names = append(names, k.Name)
	}
	return strings.Join(names, " ")
}()

var compareGuards = func() []string {
	var names []string
	for _, k := range run.GuardRecordingKeys {
		names = append(names, k.Name)
	}
	return names
}()

// Options configure the regression criterion (spec §10.1). Every field is a
// configurable default; the criterion itself is not a knob.
type Options struct {
	Policies  Policies
	SubjectOf func(*benchfmt.Result) Subject
	// Freshness is valid, stale, unverifiable, unavailable, or not-applicable.
	// It comes from the caller's shared working-tree judgment, never from samples.
	Freshness string
	// FreshnessOf supplies a per-recording verdict when comparing a corpus.
	FreshnessOf       func(*benchfmt.Result) string
	FreshnessReasonOf func(*benchfmt.Result) string
	// Blockers retain recording-level refusals while enumerating known children.
	Blockers     []Cause
	Alpha        float64         // significance level for Mann–Whitney U (default 0.05)
	ThresholdPct float64         // regression magnitude floor, in percent (default 3.0)
	Confidence   float64         // confidence level for summary intervals (default 0.95)
	GateUnits    map[string]bool // units whose regression fails the build (default {"sec/op"})
}

// DefaultOptions returns the spec's stated defaults (§9/§10.1).
func DefaultOptions() Options {
	gate := map[string]bool{}
	for _, d := range metric.Definitions() {
		if d.DefaultGate {
			gate[d.Unit] = true
		}
	}
	return Options{
		Alpha:        0.05,
		ThresholdPct: 3.0,
		Confidence:   0.95,
		GateUnits:    gate,
	}
}

// Result is the outcome of a comparison: one table per (file config, unit) plus
// notes for benchmarks that could not be compared (one-sided, missing/mixed
// provenance, variant-guard mismatch, or no metric unit present on both sides).
// The notes exist so that an un-comparable benchmark is never silently dropped.
type Result struct {
	Dispositions []Disposition
	Tables       []*Table
	Notes        []string
	// OneSided, GuardMismatch, and NoCommonUnit count the benchmarks surfaced
	// as notes instead of compared: present on one side only, blocked by a
	// missing/mixed/differing provenance guard, or two-sided with no metric
	// unit present on both sides. They let a caller whose comparison set came
	// out empty name why it is empty (spec §10.1: an empty comparison is
	// diagnosed, never silently reported clean).
	OneSided      int
	GuardMismatch int
	NoCommonUnit  int
}

// ComparedRows returns the number of (benchmark, unit) comparisons actually
// performed — the rows across every table.
func (r *Result) ComparedRows() int {
	n := 0
	for _, t := range r.Tables {
		n += len(t.Rows)
	}
	return n
}

// GatedComparisons returns how many compared rows are on gated units — the rows
// --fail-on-regression actually judges. Zero means the gate measured nothing,
// which must never read as a clean pass (spec §10.1) even though Regressed()
// is vacuously false.
func (r *Result) GatedComparisons() int {
	return r.Coverage(Policies{}).Eligible
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
	BasePackage, NewPackage   string
	Package, Recording, Label string
	Name                      string
	Base                      benchmath.Summary // median + CI of the baseline samples
	New                       benchmath.Summary // median + CI of the new samples
	Cmp                       benchmath.Comparison
	DeltaPct                  float64 // (new/base − 1)·100; NaN if the baseline center is 0
	// Regression is true iff the change is in the worse direction (higher
	// sec/op, B/op, allocs/op), statistically significant (p < α), and clears the
	// magnitude threshold — all three (spec §10.1).
	Regression bool
	// Gated names a requested metric. Eligibility in Dispositions additionally
	// decides whether its regression drives --fail-on-regression.
	Gated bool
	// Warnings carries benchmath's notes (e.g. too few samples for a finite CI),
	// surfaced rather than swallowed.
	Warnings []string
}

// Regressed reports whether any gated metric regressed — the condition that
// --fail-on-regression turns into a non-zero exit (spec §10.1).
func (r *Result) Regressed() bool {
	return r.Coverage(Policies{}).Regressed > 0
}

type cell struct{ base, newer []float64 }

// group is one benchmark under one file configuration: its two sides' samples per
// unit, plus the per-side variant guard values. Guards and presence are tracked
// at the benchmark level (not per unit) so a mismatch or one-sided benchmark
// yields a single note, not one per metric.
type group struct {
	// recordingAudit is shared by all children/configurations of an admitted
	// stored recording. Nil identifies a transient result-group audit.
	recordingAudit                                                   *group
	basePackage, newPackage                                          string
	packageName, recording, variantLabel, freshness, freshnessReason string
	name                                                             string
	config                                                           string
	hasBase, hasNew                                                  bool
	baseGuards                                                       map[string]guardValue
	newGuards                                                        map[string]guardValue
	// baseConds/newConds track the recorded `pew-runconditions` provenance per
	// side. Unlike the guards it never blocks a comparison (spec §10.1, REQ-pew-runconditions-provenance):
	// a difference is surfaced as a note and the comparison proceeds.
	baseConds, newConds guardValue
	// baseAudit/newAudit track the recorded gofresh provenance per side
	// — the vouches, the dynamic-state strategy, and the
	// attestation-borne discharge sets: two sides differing in one
	// compare with a note, never fragment (the run-conditions
	// precedent; spec §5's audit rows and the strategy row's
	// comparison clause).
	baseAudit, newAudit map[string]guardValue
	units               []string // first-seen order, deduplicated
	cells               map[string]*cell
}

type guardValue struct {
	value   string
	seen    bool
	missing bool
	mixed   bool
}

// recordGuard folds one provenance guard into a side. Empty guard values are
// missing, not wildcards: comparing an unknown machine/toolchain/buildconfig is a
// silent cross-variant comparison risk.
func recordGuard(cur guardValue, v string) guardValue {
	if v == "" {
		cur.missing = true
		return cur
	}
	if !cur.seen {
		cur.value = v
		cur.seen = true
		return cur
	}
	if cur.value != v {
		cur.mixed = true
	}
	return cur
}

// Compare runs the regression pipeline over two already-parsed result sets.
func Compare(base, newer []*benchfmt.Result, opts Options) *Result {
	return CompareProjected(base, newer, opts, run.ComparisonValues)
}

// CompareProjected compares rows using the caller's admitted configuration
// projections. The accessor belongs to this comparison and never mutates rows.
func CompareProjected(base, newer []*benchfmt.Result, opts Options, values func(*benchfmt.Result) func(string) string) *Result {
	th := &benchmath.Thresholds{CompareAlpha: opts.Alpha}
	filter := mustFilter("*")
	var parser benchproc.ProjectionParser
	configBy := mustParse(&parser, ".config", filter)
	rowBy := mustParse(&parser, ".fullname", filter)
	mustParse(&parser, pewIgnore, filter) // excluded from .config grouping

	type gkey struct {
		cfg, name benchproc.Key
		subject   Subject
	}
	groups := map[gkey]*group{}
	recordingAudits := map[string]*group{}

	add := func(rs []*benchfmt.Result, isBase bool) {
		for _, r := range rs {
			value := values(r)
			gk := gkey{cfg: configBy.Project(r), name: rowBy.Project(r)}
			var subject Subject
			if opts.SubjectOf != nil {
				subject = opts.SubjectOf(r)
				gk.subject = subject
				if subject.Location != "" {
					gk.subject.Package = ""
				}
			}
			g := groups[gk]
			if g == nil {
				g = &group{
					packageName: r.GetConfig("pkg"), recording: run.BenchName(string(r.Name)),
					name:       string(r.Name),
					config:     gk.cfg.String(),
					baseGuards: map[string]guardValue{},
					newGuards:  map[string]guardValue{},
					cells:      map[string]*cell{},
				}
				groups[gk] = g
				if opts.SubjectOf != nil {
					g.packageName, g.recording, g.variantLabel = subject.Package, subject.Recording, subject.Label
				}
			}
			guards := g.newGuards
			if isBase {
				g.hasBase = true
				g.basePackage = subject.Package
				guards = g.baseGuards
			} else {
				g.hasNew = true
				g.newPackage = subject.Package
				if opts.FreshnessOf != nil {
					g.freshness = opts.FreshnessOf(r)
				}
				if opts.FreshnessReasonOf != nil {
					g.freshnessReason = opts.FreshnessReasonOf(r)
				}
			}
			if opts.SubjectOf != nil {
				g.packageName = commonPackage(g.basePackage, g.newPackage)
			}
			if subject.Location != "" {
				audit := recordingAudits[subject.Location]
				if audit == nil {
					audit = &group{name: subject.Recording, recording: subject.Recording, variantLabel: subject.Label}
					recordingAudits[subject.Location] = audit
				}
				if isBase {
					audit.hasBase = true
					audit.basePackage = subject.Package
				} else {
					audit.hasNew = true
					audit.newPackage = subject.Package
				}
				audit.packageName = commonPackage(audit.basePackage, audit.newPackage)
				audit.recordAudit(r, value, isBase)
				g.recordingAudit = audit
			} else {
				g.recordAudit(r, value, isBase)
			}
			for _, key := range compareGuards {
				guards[key] = recordGuard(guards[key], value(key))
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
		if gs[i].packageName != gs[j].packageName {
			return gs[i].packageName < gs[j].packageName
		}
		if gs[i].recording != gs[j].recording {
			return gs[i].recording < gs[j].recording
		}
		if gs[i].variantLabel != gs[j].variantLabel {
			return gs[i].variantLabel < gs[j].variantLabel
		}
		if gs[i].config != gs[j].config {
			return gs[i].config < gs[j].config
		}
		return gs[i].name < gs[j].name
	})

	res := &Result{}
	type tkey struct{ config, unit string }
	tables := map[tkey]*Table{}
	for _, g := range gs {
		units := append([]string{}, g.units...)
		for unit, requested := range opts.GateUnits {
			if requested && g.cells[unit] == nil {
				units = append(units, unit)
			}
		}
		blockers := append([]Cause{}, opts.Blockers...)
		// One-sided: cannot compare. Surface so the omission is never silent.
		if !g.hasBase || !g.hasNew {
			side := "base"
			if !g.hasBase {
				side = "new"
			}
			res.Notes = append(res.Notes, fmt.Sprintf("%s: only present in %s; not compared", g.label(), side))
			missing := "new"
			if !g.hasBase {
				missing = "base"
			}
			blockers = append(blockers, Cause{"missing-side", missing, "only present in " + side + "; not compared"})
		}
		if note, ok := g.guardNote(); ok && g.hasBase && g.hasNew {
			res.Notes = append(res.Notes, note)
			blockers = append(blockers, g.guardCauses()...)
		}
		audit := g
		if g.recordingAudit != nil {
			audit = g.recordingAudit
		}
		condNote, hasCondNote := audit.conditionsNote()
		conditionCauses := audit.conditionCauses()
		compatible := len(conditionCauses) == 0
		rows := 0
		for _, unit := range sortUnits(units) {
			c := g.cells[unit]
			if c == nil {
				c = &cell{}
			}
			_, known := metric.Lookup(unit)
			d := Disposition{Benchmark: g.name, Config: g.config, Unit: unit, Requested: opts.GateUnits[unit] && known, Freshness: opts.Freshness, Conditions: "incompatible", Causes: append([]Cause{}, blockers...)}
			d.Package, d.Recording = g.packageName, g.recording
			d.BasePackage, d.NewPackage = g.basePackage, g.newPackage
			d.Label = g.variantLabel
			if g.freshness != "" {
				d.Freshness = g.freshness
			}
			if d.Freshness == "" {
				d.Freshness = "not-applicable"
			}
			if compatible {
				d.Conditions = "compatible"
			}
			if len(c.base) == 0 {
				d.Causes = append(d.Causes, Cause{"missing-unit", "base", "no samples for requested/observed unit"})
			}
			if len(c.newer) == 0 {
				d.Causes = append(d.Causes, Cause{"missing-unit", "new", "no samples for requested/observed unit"})
			}
			for _, side := range []struct {
				name   string
				values []float64
			}{{"base", c.base}, {"new", c.newer}} {
				for _, v := range side.values {
					if !metric.ValidSample(unit, v) {
						d.Causes = append(d.Causes, Cause{"invalid-sample", side.name, "non-finite sample or negative known cost"})
						break
					}
				}
			}
			finish := func() {
				p := opts.Policies.Effective()
				eligible := true
				if d.Freshness != "valid" && d.Freshness != "not-applicable" {
					message := d.Freshness
					if g.freshnessReason != "" {
						message += " (" + g.freshnessReason + ")"
					}
					d.Causes = append(d.Causes, Cause{"freshness", "new", message + "; comparison may not reflect HEAD"})
					if p.Freshness == "require" {
						eligible = false
					}
				}
				if !compatible && p.Conditions == "compatible" {
					eligible = false
				}
				d.Causes = append(d.Causes, conditionCauses...)
				d.Eligible = d.Compared && eligible
				d.OrderCauses()
				res.Dispositions = append(res.Dispositions, d)
				for _, cause := range d.Causes {
					if cause.Code == "invalid-sample" || cause.Code == "invalid-statistic" {
						res.Notes = append(res.Notes, fmt.Sprintf("%s %s: %s (%s side); not compared", g.label(), unit, cause.Message, cause.Side))
					}
				}
			}
			if len(c.base) == 0 || len(c.newer) == 0 {
				finish()
				continue
			}
			if len(d.Causes) > 0 {
				finish()
				continue
			}
			bs := benchmath.NewSample(c.base, th)
			ns := benchmath.NewSample(c.newer, th)
			bsum := benchmath.AssumeNothing.Summary(bs, opts.Confidence)
			nsum := benchmath.AssumeNothing.Summary(ns, opts.Confidence)
			cmp := benchmath.AssumeNothing.Compare(bs, ns)
			if !validStatistics(bsum.Center, nsum.Center, cmp.P) {
				d.Causes = append(d.Causes, Cause{"invalid-statistic", "both", "non-finite center or invalid significance probability"})
				finish()
				continue
			}
			rows++

			delta := math.NaN()
			if bsum.Center != 0 {
				delta = (nsum.Center/bsum.Center - 1) * 100
			}
			// The three independent conditions of §10.1. Magnitude is on the
			// absolute change (|Δ| ≥ threshold); direction is a separate gate, so a
			// large improvement is never a regression. A percentage is undefined
			// at zero, but a positive cost where there was none clears every
			// finite relative floor; presentation keeps that delta undefined.
			significant := cmp.P < opts.Alpha
			definition, _ := metric.Lookup(unit)
			worse := definition.HigherIsWorse && nsum.Center > bsum.Center
			magnitude := (bsum.Center == 0 && nsum.Center > 0) || math.Abs(delta) >= opts.ThresholdPct
			regression := significant && worse && magnitude
			d.Compared, d.Regression = true, regression
			finish()

			tk := tkey{g.config, unit}
			t := tables[tk]
			if t == nil {
				t = &Table{Config: g.config, Unit: unit}
				tables[tk] = t
			}
			t.Rows = append(t.Rows, Row{
				BasePackage: d.BasePackage, NewPackage: d.NewPackage,
				Package: d.Package, Recording: d.Recording, Label: d.Label,
				Name:       g.name,
				Base:       bsum,
				New:        nsum,
				Cmp:        cmp,
				DeltaPct:   delta,
				Regression: regression,
				Gated:      d.Requested,
				Warnings:   collectWarnings(bsum.Warnings, nsum.Warnings, cmp.Warnings),
			})
		}
		// Two-sided with every metric unit one-sided (disjoint unit sets):
		// no row was produced, so without a note the benchmark would vanish
		// from the output entirely — the silent drop the notes exist to
		// prevent. The run-conditions note (spec §10.1: surfaced, not gated —
		// appended while the comparison still proceeds) is emitted only when
		// rows exist: with nothing compared it would annotate a comparison
		// that never happened. The mixed-within-a-side variant is the
		// exception — it reports internally inconsistent provenance, an
		// integrity signal that stands regardless of whether anything
		// compared.
		common := false
		for _, c := range g.cells {
			if len(c.base) > 0 && len(c.newer) > 0 {
				common = true
			}
		}
		if rows == 0 && !common && len(blockers) == 0 {
			res.Notes = append(res.Notes, fmt.Sprintf("%s: no metric unit present on both sides; not compared", g.label()))
			if g.recordingAudit == nil && hasCondNote && (g.baseConds.mixed || g.newConds.mixed) {
				res.Notes = append(res.Notes, condNote)
			}
		} else if g.recordingAudit == nil && (rows > 0 || (len(blockers) > 0 && g.hasBase && g.hasNew)) && hasCondNote {
			res.Notes = append(res.Notes, condNote)
		}
		if g.recordingAudit == nil && g.hasBase && g.hasNew {
			res.Notes = append(res.Notes, g.auditNotes()...)
		}
	}
	var locations []string
	for location := range recordingAudits {
		locations = append(locations, location)
	}
	sort.Strings(locations)
	for _, location := range locations {
		audit := recordingAudits[location]
		if audit.hasBase && audit.hasNew {
			if note, ok := audit.conditionsNote(); ok {
				res.Notes = append(res.Notes, note)
			}
			res.Notes = append(res.Notes, audit.auditNotes()...)
		}
	}

	res.deriveLegacyCounts()
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
	name := g.name
	if pkg := packageLabel(g.packageName, g.basePackage, g.newPackage); pkg != "" {
		name = pkg + "." + name
	}
	if g.variantLabel != "" {
		name += " label=" + g.variantLabel
	}
	if g.config == "" {
		return name
	}
	return name + " [" + g.config + "]"
}

func (g *group) guardNote() (string, bool) {
	if causes := g.guardCauses(); len(causes) > 0 {
		return fmt.Sprintf("%s: %s; not compared", g.label(), causes[0].Message), true
	}
	return "", false
}

// conditionsNote surfaces run-condition provenance problems between the two
// sides (spec §10.1): distinct values mixed within a side, the line missing on
// one side only, or the sides differing in an observed categorical field. The
// caller never blocks the comparison on it — run conditions are provenance, not
// a guard (REQ-pew-runconditions-provenance). Both sides lacking the line entirely is silent: there is
// nothing recorded to disagree about.
func (g *group) conditionsNote() (string, bool) {
	return auditNote(g.label(), run.KeyRunConditions.Display, g.baseConds, g.newConds, conditionsDiffer)
}

// conditionCategoricalFields are the recorded run-condition fields whose
// difference triggers a note. load1 is deliberately absent (spec §10.1): a
// continuous load average differs between almost any two runs, so it is
// recorded context, never a trigger.
var conditionCategoricalFields = []string{"governor", "turbo", "throttled", "battery"}

// auditNoteKeys are the registry's audit-marked lines (spec §5's
// `audit?` column) other than the run conditions, whose note reads
// categorical fields (conditionsNote, §10.1): audit provenance, so a
// note carrying the row's display name, never a grouping key. A side
// where some samples carry a line and some omit it is itself mixed
// provenance and reports as such, so a partially-recorded side can
// never silently read as one value.
var auditNoteKeys = func() []run.RecordingKey {
	var keys []run.RecordingKey
	for _, k := range run.AuditRecordingKeys {
		if k.Name != run.KeyRunConditions.Name {
			keys = append(keys, k)
		}
	}
	return keys
}()

// auditNotes surfaces sides whose recorded gofresh provenance differs —
// one mechanism for all five lines. For the dynamic-state strategy line
// the note is the surface for every REF-RESOLVED side (a pinned tag,
// auto's HEAD, either A/B ref): those sides always compare — stat skips
// only the working-tree side before Compare sees it (spec §5's
// dynamic-state row) — so a cross-strategy comparison lands here. The
// closure-derivation line is audit with no skip on any side, so its
// note is the surface for every side. The mixed-within-a-side arm
// additionally catches rows disagreeing past the first, which the
// skip's row-0 gate cannot see.
func (g *group) auditNotes() []string {
	var notes []string
	for _, k := range auditNoteKeys {
		if note, ok := auditNote(g.label(), k.Display, g.baseAudit[k.Name], g.newAudit[k.Name], func(a, b string) bool { return a != b }); ok {
			notes = append(notes, note)
		}
	}
	return notes
}

// recordAudit folds the same evidence for either a stored recording or a
// transient result group. It has no numerical matching or obligation semantics.
func (g *group) recordAudit(r *benchfmt.Result, value func(string) string, isBase bool) {
	conditions, audits := &g.newConds, &g.newAudit
	if isBase {
		conditions, audits = &g.baseConds, &g.baseAudit
	}
	*conditions = recordGuard(*conditions, r.GetConfig(run.KeyRunConditions.Name))
	if *audits == nil {
		*audits = map[string]guardValue{}
	}
	for _, key := range auditNoteKeys {
		(*audits)[key.Name] = recordGuard((*audits)[key.Name], value(key.Name))
	}
}

func auditNote(label, name string, base, newer guardValue, differ func(string, string) bool) (string, bool) {
	bm, nm := base.mixed || (base.seen && base.missing), newer.mixed || (newer.seen && newer.missing)
	if bm || nm {
		side := "both sides"
		if !nm {
			side = "the base side"
		}
		if !bm {
			side = "the new side"
		}
		return fmt.Sprintf("%s: mixed %s within %s", label, name, side), true
	}
	b, n := base.seen && !base.missing, newer.seen && !newer.missing
	if !b && !n {
		return "", false
	}
	if !b {
		return fmt.Sprintf("%s: %s differ (base: (none); new: %s); %s unrecorded on base side", label, name, newer.value, name), true
	}
	if !n {
		return fmt.Sprintf("%s: %s differ (base: %s; new: (none)); %s unrecorded on new side", label, name, base.value, name), true
	}
	if differ(base.value, newer.value) {
		return fmt.Sprintf("%s: %s differ (base: %s; new: %s)", label, name, base.value, newer.value), true
	}
	return "", false
}

func (g *group) conditionCauses() []Cause {
	b, n := g.baseConds, g.newConds
	var causes []Cause
	for _, side := range []struct {
		name  string
		value guardValue
	}{{"base", b}, {"new", n}} {
		v := side.value
		if v.mixed || (v.seen && v.missing) {
			causes = append(causes, Cause{"conditions", side.name, "mixed run conditions"})
			continue
		}
		if !v.seen || v.missing {
			causes = append(causes, Cause{"conditions", side.name, "run conditions unrecorded"})
			continue
		}
		fields := conditionCategorical(v.value)
		for _, f := range conditionCategoricalFields {
			if !run.KnownConditionValue(f, fields[f]) {
				causes = append(causes, Cause{"conditions", side.name, f + " is unknown, malformed, or mixed"})
			}
		}
	}
	bv, nv := conditionCategorical(b.value), conditionCategorical(n.value)
	for _, f := range conditionCategoricalFields {
		if b.seen && n.seen && !b.mixed && !n.mixed && !b.missing && !n.missing && run.KnownConditionValue(f, bv[f]) && run.KnownConditionValue(f, nv[f]) && bv[f] != nv[f] {
			causes = append(causes, Cause{"conditions", "both", fmt.Sprintf("%s differs (base: %s; new: %s)", f, bv[f], nv[f])})
		}
	}
	return causes
}

func (g *group) guardCauses() []Cause {
	var causes []Cause
	for _, key := range compareGuards {
		b, n := g.baseGuards[key], g.newGuards[key]
		sides := []struct {
			name string
			v    guardValue
		}{{"base", b}, {"new", n}}
		for _, s := range sides {
			if s.v.mixed {
				causes = append(causes, Cause{"guard", s.name, "mixed " + key + " provenance"})
			}
		}
		for _, s := range sides {
			if s.v.missing || !s.v.seen {
				causes = append(causes, Cause{"guard", s.name, "missing " + key + " provenance"})
			}
		}
		if b.seen && n.seen && !b.mixed && !n.mixed && !b.missing && !n.missing && b.value != n.value {
			causes = append(causes, Cause{"guard", "both", key + " mismatch"})
		}
	}
	return causes
}

// conditionsDiffer compares two recorded `pew-runconditions` values on their
// categorical fields. Parsing is fail-closed for hand-edited recordings: a
// missing or malformed field reads as "unknown", so garbage never silently
// equals an observed value — and two identically-unknown sides (e.g. two
// non-Linux recordings) do not differ.
func conditionsDiffer(base, newer string) bool {
	b, n := conditionCategorical(base), conditionCategorical(newer)
	for _, field := range conditionCategoricalFields {
		if b[field] != n[field] {
			return true
		}
	}
	return false
}

func conditionCategorical(value string) map[string]string {
	out := make(map[string]string, len(conditionCategoricalFields))
	for _, field := range conditionCategoricalFields {
		out[field] = "unknown"
	}
	// A repeated or malformed occurrence of a tracked field collapses it to
	// "unknown" (fail-closed, mirroring §5's duplicate-key posture) rather than
	// letting any occurrence silently equal an observed value. The occurrence
	// bookkeeping runs before the token-validity filter so a malformed repeat
	// ("governor=powersave governor=") still poisons the field.
	seen := map[string]bool{}
	for _, token := range strings.Fields(value) {
		key, v, ok := strings.Cut(token, "=")
		if _, tracked := out[key]; !tracked {
			continue
		}
		if seen[key] || !ok || v == "" {
			seen[key] = true
			out[key] = "unknown"
			continue
		}
		seen[key] = true
		if run.KnownConditionValue(key, v) || (key == "governor" && v == "mixed") {
			out[key] = v
		}
	}
	return out
}

// unitRank uses the metric registry's display order; unknown units follow it.
func unitRank(u string) int {
	return metric.Rank(u)
}

// validStatistics admits the arithmetic results separately from their finite
// input samples. Neither a library overflow nor a NaN probability is evidence.
func validStatistics(base, newer, p float64) bool {
	return metric.Finite(base) && metric.Finite(newer) && metric.Finite(p) && p >= 0 && p <= 1
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
			if !metric.Finite(row.DeltaPct) {
				delta = "undefined (" + row.Cmp.String() + ")"
			}
			if row.Regression {
				delta += "  ⚠ regression"
			}
			name := row.Name
			if pkg := packageLabel(row.Package, row.BasePackage, row.NewPackage); pkg != "" {
				name = pkg + "." + name
			}
			if row.Label != "" {
				name += " label=" + row.Label
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", name, base, newv, delta)
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
