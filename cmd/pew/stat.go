package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"path/filepath"
	"sort"
	"strings"

	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/pew/internal/compare"
	"github.com/greatliontech/pew/internal/gitblob"
	"github.com/greatliontech/pew/internal/gotool"
	"github.com/greatliontech/pew/internal/metric"
	"github.com/greatliontech/pew/internal/profiles"
	runpkg "github.com/greatliontech/pew/internal/run"
	"github.com/greatliontech/pew/internal/store"
	"github.com/spf13/cobra"
	"golang.org/x/mod/modfile"
	"golang.org/x/perf/benchfmt"
)

type statConfig struct {
	profile          string
	vouches          []string
	benchDir         string
	label            string
	opts             compare.Options
	failOnRegression bool
	explain          bool
	jsonOut          bool
}

func newStatCmd() *cobra.Command {
	var sc statConfig
	sc.opts = compare.DefaultOptions()
	var gate string
	cmd := &cobra.Command{
		Use:   "stat [ref | refA refB]",
		Short: guidanceShort("stat"),
		Long:  guidanceHelp("stat"),
		Args:  cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			gu, err := parseGateUnits(gate)
			if err != nil {
				return err
			}
			sc.opts.GateUnits = gu
			if err := validateOptions(sc.opts); err != nil {
				return err
			}
			if err := sc.opts.Policies.Validate(); err != nil {
				return err
			}
			if sc.explain && sc.jsonOut {
				return fmt.Errorf("stat: --explain and -json are mutually exclusive (the explanation is a human view)")
			}
			if err := store.ValidateLabel(sc.label); err != nil {
				return err
			}
			ctx, stop := commandContext(cmd)
			defer stop()
			return runStat(ctx, cmd.OutOrStdout(), cmd.ErrOrStderr(), sc, args)
		},
	}
	f := cmd.Flags()
	f.StringVar(&sc.profile, "profile", "", "compare requested diagnostic profiles")
	f.Lookup("profile").NoOptDefVal = "cpu,alloc"
	f.StringVar(&sc.benchDir, "bench-dir", "", "")
	f.StringVar(&sc.label, "label", "", "")
	f.Float64Var(&sc.opts.Alpha, "alpha", sc.opts.Alpha, "")
	f.Float64Var(&sc.opts.ThresholdPct, "threshold", sc.opts.ThresholdPct, "")
	f.Float64Var(&sc.opts.Confidence, "confidence", sc.opts.Confidence, "")
	f.BoolVar(&sc.failOnRegression, "fail-on-regression", false, "")
	f.BoolVar(&sc.explain, "explain", false, "")
	f.BoolVar(&sc.jsonOut, "json", false, "")
	var defaultGate []string
	for _, d := range metric.Definitions() {
		if d.DefaultGate {
			defaultGate = append(defaultGate, d.Unit)
		}
	}
	f.StringVar(&gate, "gate", strings.Join(defaultGate, ","), "")
	f.StringVar(&sc.opts.Policies.Coverage, "coverage", "partial", "")
	f.StringVar(&sc.opts.Policies.Freshness, "freshness", "report", "")
	f.StringVar(&sc.opts.Policies.Conditions, "conditions", "report", "")
	f.StringArrayVar(&sc.vouches, "vouch", nil, "")
	return cmd
}

// validateOptions rejects out-of-range tunables that would silently corrupt the
// regression criterion (spec §10.1): α and confidence outside (0,1), or a
// negative magnitude floor (which would make |Δ| ≥ threshold always true and gut
// condition (3)). A zero threshold is allowed — it means "any significant worse
// change regresses", a legitimate (noisier) choice.
func validateOptions(o compare.Options) error {
	if math.IsNaN(o.Alpha) || o.Alpha <= 0 || o.Alpha >= 1 {
		return fmt.Errorf("stat: --alpha must be in (0,1), got %v", o.Alpha)
	}
	if math.IsNaN(o.Confidence) || o.Confidence <= 0 || o.Confidence >= 1 {
		return fmt.Errorf("stat: --confidence must be in (0,1), got %v", o.Confidence)
	}
	if math.IsNaN(o.ThresholdPct) || math.IsInf(o.ThresholdPct, 0) || o.ThresholdPct < 0 {
		return fmt.Errorf("stat: --threshold must be finite and ≥ 0, got %v", o.ThresholdPct)
	}
	return nil
}

func parseGateUnits(s string) (map[string]bool, error) {
	out := map[string]bool{}
	for _, tok := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' }) {
		if _, known := metric.Lookup(tok); !known {
			var allowed []string
			for _, d := range metric.Definitions() {
				allowed = append(allowed, d.Unit)
			}
			return nil, fmt.Errorf("stat: unknown --gate unit %q (want %s)", tok, strings.Join(allowed, ", "))
		}
		out[tok] = true
	}
	if len(out) == 0 {
		return nil, errors.New("stat: --gate must list at least one unit")
	}
	return out, nil
}

// nothingComparedError is the --fail-on-regression empty-comparison failure
// (spec §10.1): the gate measured nothing on any gated unit, so a clean-pass
// exit would be vacuous — the gate would pass precisely when it measured
// nothing. main maps it to a distinct exit status so CI can tell "compared and
// clean" (0), "regression detected" (1), and "nothing compared" (2) apart.
type nothingComparedError struct {
	reason     string
	incomplete bool
}

func (e *nothingComparedError) Error() string {
	if e.incomplete {
		return "fail-on-regression: incomplete required coverage — " + e.reason
	}
	return "fail-on-regression: nothing compared — " + e.reason
}

// baseline names the two sides of a comparison. newRef == "" means the new side
// is the working-tree recording (the latest `pew run`), giving "git diff
// semantics" for the auto and pinned modes (spec §10).
type baseline struct{ baseRef, newRef string }

type statKey struct {
	pkgRel string
	bench  string
	label  string
}

type currentBench struct {
	importPath string
	moduleDir  string
	pkgDir     string
	mainPkg    bool
}

type statModule struct {
	refModules        map[string]string
	inventoryFailures []compare.Disposition
	modulePath        string
	moduleDir         string
	benchDir          string
	store             *store.Store
	repo              *gitblob.Repo
	keys              map[statKey]bool
	current           map[statKey]currentBench
	sides             map[statSideKey]statSide
}

type statSideKey struct {
	ref, pkgRel, bench, label string
}

type statSide struct {
	recs     []*benchfmt.Result
	ok       bool
	err      error
	admitted *admission
}

func baselineFor(refs []string) (baseline, error) {
	switch len(refs) {
	case 0:
		return baseline{baseRef: "HEAD", newRef: ""}, nil
	case 1:
		return baseline{baseRef: refs[0], newRef: ""}, nil
	case 2:
		return baseline{baseRef: refs[0], newRef: refs[1]}, nil
	default:
		return baseline{}, fmt.Errorf("stat: at most two refs (got %d)", len(refs))
	}
}

func (b baseline) historicalRefs() []string {
	refs := []string{b.baseRef}
	if b.newRef != "" {
		refs = append(refs, b.newRef)
	}
	return refs
}

func runStat(ctx context.Context, w, errw io.Writer, sc statConfig, refs []string) error {
	kinds, err := profiles.Kinds(sc.profile)
	if err != nil {
		return err
	}
	var profileReports []profileComparison
	if err := sc.opts.Policies.Validate(); err != nil {
		return err
	}
	var withheld []compare.Disposition
	var withheldNotes []string
	withhold := func(r *compare.Result) {
		withheld = append(withheld, r.Dispositions...)
		withheldNotes = append(withheldNotes, r.Notes...)
	}
	var reportErrors []error
	ctx, invocation, err := beginInvocation(ctx, sc.benchDir, sc.vouches, errw)
	if err != nil {
		return err
	}
	env := invocation.env
	sc.benchDir = invocation.vouches.storeDir
	reportPhase(ctx, "listing")
	bl, err := baselineFor(refs)
	if err != nil {
		return err
	}
	pkgs, err := statPackages(ctx, env, bl, errw, &withheld)
	if err != nil {
		if cancelledBy(ctx, err) {
			return interrupted("stat: interrupted while listing packages")
		}
		return err
	}
	repo, err := gitblob.Open(".")
	if err != nil {
		return err
	}
	repo, err = repo.SnapshotRefs(bl.historicalRefs()...)
	if err != nil {
		return err
	}

	modules, err := statModules(pkgs, sc, errw)
	if err != nil {
		return err
	}
	frozenRepos := map[string]*gitblob.Repo{repo.Root(): repo}
	for _, m := range modules {
		frozen := frozenRepos[m.repo.Root()]
		if frozen == nil {
			frozen, err = m.repo.SnapshotRefs(bl.historicalRefs()...)
			if err != nil {
				return err
			}
			frozenRepos[m.repo.Root()] = frozen
		}
		m.repo = frozen
	}
	scanRoots, err := historicalScanRoots(modules)
	if err != nil {
		return err
	}
	reportPhase(ctx, "scanning history")
	modules, err = addHistoricalModules(ctx, modules, repo, bl.historicalRefs(), sc, scanRoots)
	if err != nil {
		return err
	}
	modules = dedupeStatModules(modules)
	var baseAll, newAll []*benchfmt.Result

	// When the new side is the working-tree recording (auto/pinned), a recording
	// that is stale for HEAD does not reflect current code, so its "new" numbers
	// are misleading — warn (don't block), pointing at `pew run`. The engine is
	// only needed for that check, so it is built only then — through the shared
	// construction path, so stat honors //gofresh:pure directives and the
	// per-package PGO build input exactly as status and run do (§7.5, §9).
	// Engines are cached per (module, PGO input): packages of one module whose
	// effective profiles differ need different guard inputs.
	analysis := newStatAnalysis(env)
	judgments := map[string]map[string]*benchVerdict{}
	projections := map[*benchfmt.Result]gofresh.Fingerprint{}
	subjects := map[*benchfmt.Result]compare.Subject{}
	freshness := map[*benchfmt.Result]string{}
	freshnessReason := map[*benchfmt.Result]string{}

	for mi, m := range modules {
		if err := ctx.Err(); err != nil {
			return interrupted("stat: interrupted before module %s (%d/%d); nothing compared", m.modulePath, mi+1, len(modules))
		}
		reportPhase(ctx, fmt.Sprintf("reading recordings of %s (%d/%d)", m.modulePath, mi+1, len(modules)))
		if err := addStatInventory(m, bl, sc.label); err != nil {
			if cancelledBy(ctx, err) {
				return interrupted("stat: interrupted while inventorying recordings")
			}
			reportErrors = append(reportErrors, err)
			withheld = append(withheld, compare.Disposition{Package: m.modulePath, Requested: true, Causes: []compare.Cause{{Code: "inventory", Message: "recording inventory unavailable"}}})
		}
		if bl.newRef == "" {
			withheld = append(withheld, m.inventoryFailures...)
		}
		if bl.newRef == "" {
			for key := range m.current {
				m.keys[key] = true
			}
		}
		// newSideIsWorkingTree is the per-side property both consumers
		// derive from: the new side is the working tree exactly when no
		// new ref is pinned — that side is re-recordable and is the one
		// whose freshness verdict is computed (the engine's staleness
		// arm and the strategy skip both key on it; ref-resolved sides
		// get neither).
		newSideIsWorkingTree := bl.newRef == ""

		keys := sortedStatKeys(m.keys)
		for ki, key := range keys {
			stoppedAt := func() error {
				return interrupted("stat: interrupted at %s.%s (%d/%d in %s); nothing compared — the comparison is one shot over the whole corpus", key.pkgRel, key.bench, ki+1, len(keys), m.modulePath)
			}
			if ctx.Err() != nil {
				return stoppedAt()
			}
			reportPhase(ctx, fmt.Sprintf("judging %s.%s (%d/%d in %s)", key.pkgRel, key.bench, ki+1, len(keys), m.modulePath))
			if ctx.Err() != nil {
				return stoppedAt()
			}
			baseRecs, baseOK, err := m.readSide(bl.baseRef, key.pkgRel, key.bench, key.label)
			baseReadErr := err
			newRecs, newOK, err := m.readSide(bl.newRef, key.pkgRel, key.bench, key.label)
			newReadErr := err
			if cancelledBy(ctx, baseReadErr) || cancelledBy(ctx, newReadErr) {
				return stoppedAt()
			}
			resolved, err := m.resolveSubjects(key, bl)
			if err != nil {
				return err
			}
			if !baseOK && !newOK && baseReadErr == nil && newReadErr == nil {
				withhold(blockedStatUnits(m, key, bl, resolved, sc.opts, []compare.Cause{{Code: "missing-side", Side: "both", Message: "unrecorded on both sides"}}))
				continue
			}
			// Each side's stale-format state warns and tallies independently
			// (spec §10.1 per-side; the tally counts recording files), so with
			// both sides stale neither file goes unmentioned.
			// One admissibility ladder for every surface (admitRecording):
			// the format rung on both sides, the strategy rung on the
			// working-tree side alone.
			baseAdm, newAdm := m.admission(bl.baseRef, key), m.admission(bl.newRef, key)
			profileReports = append(profileReports, compareStatProfiles(ctx, m, key, bl, kinds, resolved, analysis)...)
			var causes []compare.Cause
			for _, side := range []struct {
				name       string
				present    bool
				adm        admission
				historical bool
				rows       []*benchfmt.Result
				err        error
			}{
				{"base", baseOK, baseAdm, true, baseRecs, baseReadErr}, {"new", newOK, newAdm, bl.newRef != "", newRecs, newReadErr},
			} {
				if side.err != nil && !errors.Is(side.err, store.ErrInvalidRecording) {
					reportErrors = append(reportErrors, side.err)
					causes = append(causes, compare.Cause{Code: "inventory", Side: side.name, Message: "recording could not be read"})
					continue
				}
				if !side.present {
					causes = append(causes, compare.Cause{Code: "missing-side", Side: side.name, Message: "recording absent"})
					continue
				}
				if !side.adm.ok {
					code := "format"
					if side.adm.class == "dynamic-state strategy" {
						code = "strategy"
					}
					causes = append(causes, compare.Cause{Code: code, Side: side.name, Message: "stale (" + side.adm.class + ")"})
				}
				if side.adm.class != "format" && side.historical && isDirty(side.rows) {
					causes = append(causes, compare.Cause{Code: "dirty-ref", Side: side.name, Message: "dirty historical recording"})
				}
			}
			blocked := false
			for _, c := range causes {
				if c.Code != "missing-side" {
					blocked = true
				}
			}
			if blocked {
				withhold(blockedStatUnits(m, key, bl, resolved, sc.opts, causes))
			}
			readFailed := false
			for _, c := range causes {
				if c.Code == "inventory" {
					readFailed = true
				}
			}
			if readFailed {
				continue
			}
			for _, r := range newRecs {
				if newSideIsWorkingTree {
					freshness[r] = "unavailable"
				} else {
					freshness[r] = "not-applicable"
				}
			}
			for _, adm := range []admission{baseAdm, newAdm} {
				if adm.ok {
					for _, row := range adm.rows {
						projections[row] = adm.fp
					}
				}
			}
			resolved.bind(subjects, baseRecs, newRecs)
			baseStale := baseOK && !baseAdm.ok && baseAdm.class == "format"
			newStale := newOK && !newAdm.ok && newAdm.class == "format"
			if baseStale {
				fmt.Fprintf(errw, "pew: warning: baseline %s:%s is stale (format); skipping — re-run `pew run`\n", bl.baseRef, key.bench)
			}
			if newStale {
				side := bl.newRef
				if side == "" {
					side = "working-tree"
				}
				fmt.Fprintf(errw, "pew: warning: %s recording %s.%s is stale (format); skipping — re-run `pew run`\n", side, key.pkgRel, key.bench)
			}
			if baseStale || newStale {
				continue
			}
			// A recording under another dynamic-state strategy reads fine
			// and its measured numbers are strategy-independent — only its
			// freshness verdict is not this engine's. The skip therefore
			// cuts PER SIDE: the working-tree side (re-recordable and the
			// verdict-bearing side, present exactly when
			// newSideIsWorkingTree) skips and re-records; a ref-resolved
			// side — a pinned tag, auto's
			// HEAD, either A/B ref — cannot be re-run into and always
			// compares, a strategy difference surfacing as compare's audit
			// note (spec §5's strategy row, scoped by §7's exclusion). The
			// base side enters no engine verdict on any path, so there is
			// no laundering channel to guard there.
			if newOK && !newAdm.ok && newAdm.class == "dynamic-state strategy" {
				fmt.Fprintf(errw, "pew: warning: working-tree recording %s.%s is stale (dynamic-state strategy); skipping — re-run `pew run`\n", key.pkgRel, key.bench)
				continue
			}
			// A dirty recording's commit does not faithfully describe its source
			// (§5), so it is never usable as a baseline (§5, §10: "Pinned refs must
			// resolve to non-dirty recordings"). A baseline always comes from a ref
			// (base side in every mode; the new side too in A/B); the working-tree
			// side (newRef=="") is the code under test and may be dirty. Skip a
			// dirty baseline rather than report a verdict against unfaithful
			// numbers — each dirty side warns and tallies, like stale format.
			baseDirty := baseOK && isDirty(baseRecs)
			newDirty := newOK && bl.newRef != "" && isDirty(newRecs)
			if baseDirty {
				fmt.Fprintf(errw, "pew: warning: baseline %s:%s is a dirty recording; skipping (spec §10)\n", bl.baseRef, key.bench)
			}
			if newDirty {
				fmt.Fprintf(errw, "pew: warning: new side %s:%s is a dirty recording; skipping (spec §10)\n", bl.newRef, key.bench)
			}
			if baseDirty || newDirty {
				continue
			}
			if newSideIsWorkingTree && newOK {
				cur, ok := m.current[key]
				if !ok {
					fmt.Fprintf(errw, "pew: warning: working-tree recording %s.%s has no current benchmark declaration; comparison may not reflect HEAD — re-run `pew run`\n", key.pkgRel, key.bench)
					for _, r := range newRecs {
						freshnessReason[r] = "no current benchmark declaration"
					}
				} else {
					// Best-effort: a check failure warns but never blocks the
					// comparison. The per-side stale-format gate above already
					// guarantees a decodable fingerprint on this side.
					prepared := analysis.prepare(ctx, cur)
					if cancelledBy(ctx, prepared.err) {
						return stoppedAt()
					}
					if prepared.err != nil {
						if prepared.fatal {
							return prepared.err
						}
						fmt.Fprintf(errw, "pew: warning: %s.%s: cannot check working-tree staleness: %v\n", cur.importPath, key.bench, prepared.err)
						for _, r := range newRecs {
							freshnessReason[r] = prepared.reason
						}
						baseAll = append(baseAll, baseRecs...)
						newAll = append(newAll, newRecs...)
						continue
					}
					// The shared verdict core applies the inert-growth rule
					// exactly as status and run (its default filter) do (spec §7.9); stat
					// stays read-only, so the returned ledger is dropped and
					// no recording is rewritten here.
					warnForeignKeys(errw, cur.importPath, key.bench, store.ForeignConfigKeys(newRecs))
					batchKey := cur.moduleDir + "\x00" + cur.importPath + "\x00" + key.label
					batch := judgments[batchKey]
					if batch == nil {
						batch, err = m.judgePackage(ctx, analysis, cur, key.label)
						if cancelledBy(ctx, err) {
							return stoppedAt()
						}
						if err != nil {
							return err
						}
						judgments[batchKey] = batch
					}
					judged := batch[key.bench]
					v, reason, fp, e := judged.v, judged.reason, judged.fp, judged.err
					if e == nil {
						for _, r := range newRecs {
							freshness[r] = string(v)
							freshnessReason[r] = reason
						}
					}
					if cancelledBy(ctx, e) {
						return stoppedAt()
					} else if e != nil {
						fmt.Fprintf(errw, "pew: warning: %s.%s: cannot check working-tree staleness: %v\n", cur.importPath, key.bench, e)
						for _, r := range newRecs {
							freshnessReason[r] = "freshness check failed"
						}
					} else if v != verdictValid {
						msg := string(v)
						if reason != "" {
							msg += " (" + reason + ")"
						}
						fmt.Fprintf(errw, "pew: warning: working-tree recording %s.%s is %s; comparison may not reflect HEAD — re-run `pew run`\n", cur.importPath, key.bench, msg)
						if sc.explain {
							if err := explainCapturedRecord(ctx, errw, cur.moduleDir, fp, judged.current, env.Values()); err != nil {
								if cancelledBy(ctx, err) {
									return stoppedAt()
								}
								// Staleness explanation is best-effort for stat; its
								// analysis diagnostic already printed. Output loss is not.
								var outputErr *explanationOutputError
								if errors.As(err, &outputErr) {
									return err
								}
							}
						}
					}
				}
			}
			if sc.explain && baseOK && newOK {
				a, b := baseAdm.fp.Guards, newAdm.fp.Guards
				if baseAdm.ok && newAdm.ok && a != b {
					newLabel := bl.newRef
					if newLabel == "" {
						newLabel = "working-tree"
					}
					fmt.Fprintf(errw, "pew: explain: %s.%s guard mismatch between %s and %s:\n", key.pkgRel, key.bench, bl.baseRef, newLabel)
					if err := writeExplainRows(errw, "base", "new", guardRows(a, b)); err != nil {
						return err
					}
				}
			}
			baseAll = append(baseAll, baseRecs...)
			newAll = append(newAll, newRecs...)
		}
	}

	sc.opts.FreshnessOf = func(r *benchfmt.Result) string { return freshness[r] }
	sc.opts.SubjectOf = func(r *benchfmt.Result) compare.Subject { return subjects[r] }
	sc.opts.FreshnessReasonOf = func(r *benchfmt.Result) string { return freshnessReason[r] }
	res := compare.CompareProjected(baseAll, newAll, sc.opts, func(row *benchfmt.Result) func(string) string {
		fp := projections[row]
		return func(key string) string {
			if runpkg.IsFingerprintProjection(key) {
				return runpkg.FingerprintValue(fp, key)
			}
			return row.GetConfig(key)
		}
	})
	res.Dispositions = append(res.Dispositions, withheld...)
	res.Notes = append(res.Notes, withheldNotes...)
	for i := range res.Dispositions {
		res.Dispositions[i].Label = sc.label
	}
	coverage := res.Coverage(sc.opts.Policies)
	if sc.jsonOut {
		if err := writeStatReportJSON(w, res, coverage); err != nil {
			return err
		}
	} else if err := writeStatReportText(w, res, coverage); err != nil {
		return err
	}
	if err := writeProfileComparisons(w, profileReports, sc.jsonOut); err != nil {
		return err
	}
	for _, report := range profileReports {
		if report.failure != nil {
			reportErrors = append(reportErrors, report.failure)
		}
	}
	if err := interruptedAfterLastUnit(ctx, "stat: interrupted after the last comparison; the comparison shown is complete"); err != nil {
		return err
	}
	if len(reportErrors) > 0 {
		return errors.Join(reportErrors...)
	}
	if sc.failOnRegression && res.Regressed() {
		return errors.New("regression detected")
	}
	if len(kinds) != 0 {
		fulfilled := len(profileReports) != 0
		for _, r := range profileReports {
			fulfilled = fulfilled && r.Compared
		}
		if !fulfilled {
			return &profileUnfulfilledError{}
		}
	}
	if !sc.failOnRegression {
		return nil
	}
	// The gate never passes vacuously (spec §10.1): with zero gated-unit
	// comparisons there is nothing for Regressed() to judge, so a clean exit
	// would report "no regression" over a set that was never measured. Compared
	// rows govern a partial comparison; complete additionally requires every
	// requested obligation to be eligible and compared.
	if !coverage.Satisfied {
		return &nothingComparedError{reason: res.EmptyReason(sc.opts.Policies), incomplete: coverage.Eligible > 0}
	}
	return nil
}

func statPackages(ctx context.Context, env gotool.Environment, bl baseline, errw io.Writer, failures *[]compare.Disposition) ([]pkgMeta, error) {
	pkgs, err := resolvePackages(ctx, env, []string{"./..."})
	if err != nil {
		if bl.newRef == "" {
			*failures = append(*failures, compare.Disposition{Requested: true, Causes: []compare.Cause{{Code: "inventory", Side: "new", Message: "current package inventory unavailable"}}})
		}
		fallback, fallbackErr := fallbackStatPackages(ctx, env)
		if fallbackErr != nil {
			if bl.newRef != "" {
				fmt.Fprintf(errw, "pew: warning: current package inventory unavailable: %v\n", err)
				return nil, nil
			}
			return nil, err
		}
		fmt.Fprintf(errw, "pew: warning: current package inventory unavailable: %v\n", err)
		return fallback, nil
	}
	if len(pkgs) != 0 {
		return pkgs, nil
	}
	fallback, err := fallbackStatPackages(ctx, env)
	if err != nil {
		if bl.newRef != "" {
			return nil, nil
		}
		return nil, err
	}
	return fallback, nil
}

func statModules(pkgs []pkgMeta, sc statConfig, errw io.Writer) ([]*statModule, error) {
	byDir := map[string]*statModule{}
	for _, p := range pkgs {
		if p.Module.Dir == "" {
			continue // not in a module (e.g. a stdlib pattern) — nothing recorded
		}
		m := byDir[p.Module.Dir]
		if m == nil {
			dir, err := moduleBenchDir(sc.benchDir, p.Module.Dir)
			if err != nil {
				return nil, err
			}
			repo, err := gitblob.Open(p.Module.Dir)
			if err != nil {
				return nil, err
			}
			m = &statModule{
				modulePath: p.Module.Path,
				moduleDir:  p.Module.Dir,
				benchDir:   dir,
				store:      store.New(dir),
				repo:       repo,
				keys:       map[statKey]bool{},
				current:    map[statKey]currentBench{},
			}
			byDir[p.Module.Dir] = m
		}
		benches, err := selectedBenchmarks(p)
		if err != nil {
			// Consistent with status/run: a package whose benchmark declarations cannot
			// be read is reported and skipped, not fatal to the whole comparison.
			fmt.Fprintf(errw, "pew: warning: %s: %v\n", p.ImportPath, err)
			m.inventoryFailures = append(m.inventoryFailures, compare.Disposition{Package: p.ImportPath, Requested: true, Causes: []compare.Cause{{Code: "inventory", Side: "new", Message: "benchmark declarations unavailable"}}})
			continue
		}
		pkgRel := packageRel(p)
		for _, b := range benches {
			m.current[statKey{pkgRel: pkgRel, bench: b, label: sc.label}] = currentBench{importPath: p.ImportPath, moduleDir: p.Module.Dir, pkgDir: p.Dir, mainPkg: p.Name == "main"}
		}
	}
	mods := make([]*statModule, 0, len(byDir))
	for _, m := range byDir {
		mods = append(mods, m)
	}
	sort.Slice(mods, func(i, j int) bool { return mods[i].moduleDir < mods[j].moduleDir })
	return mods, nil
}

func historicalScanRoots(mods []*statModule) ([]string, error) {
	seen := map[string]bool{}
	var roots []string
	for _, m := range mods {
		root := filepath.Clean(m.moduleDir)
		if !seen[root] {
			seen[root] = true
			roots = append(roots, root)
		}
	}
	if len(roots) == 0 {
		cwd, err := filepath.Abs(".")
		if err != nil {
			return nil, err
		}
		roots = append(roots, filepath.Clean(cwd))
	}
	sort.Strings(roots)
	return roots, nil
}

func addHistoricalModules(ctx context.Context, mods []*statModule, repo *gitblob.Repo, refs []string, sc statConfig, scanRoots []string) ([]*statModule, error) {
	byDir := map[string]*statModule{}
	for _, m := range mods {
		byDir[m.moduleDir] = m
	}
	for _, ref := range refs {
		for _, root := range scanRoots {
			if err := ctx.Err(); err != nil {
				return nil, interrupted("stat: interrupted scanning %s at %s; nothing compared", root, ref)
			}
			reportPhase(ctx, fmt.Sprintf("scanning %s at %s", root, ref))
			paths, err := repo.ListAt(ref, root)
			if err != nil {
				return nil, err
			}
			for _, path := range paths {
				if filepath.Base(path) != "go.mod" {
					continue
				}
				moduleDir := filepath.Dir(path)
				if _, ok := byDir[moduleDir]; ok {
					continue
				}
				content, ok, err := repo.ReadAt(ref, path)
				if err != nil {
					return nil, err
				}
				if !ok {
					continue
				}
				f, err := modfile.Parse(path, content, nil)
				if err != nil || f.Module == nil {
					continue
				}
				benchDir, err := moduleBenchDir(sc.benchDir, moduleDir)
				if err != nil {
					return nil, err
				}
				byDir[moduleDir] = &statModule{
					modulePath: f.Module.Mod.Path,
					moduleDir:  moduleDir,
					benchDir:   benchDir,
					store:      store.New(benchDir),
					repo:       repo,
					keys:       map[statKey]bool{},
					current:    map[statKey]currentBench{},
				}
			}
		}
	}
	out := make([]*statModule, 0, len(byDir))
	for _, m := range byDir {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].moduleDir < out[j].moduleDir })
	return out, nil
}

func dedupeStatModules(mods []*statModule) []*statModule {
	byStore := map[string]*statModule{}
	var out []*statModule
	for _, m := range mods {
		key := filepath.Clean(m.benchDir)
		if existing := byStore[key]; existing != nil {
			existing.inventoryFailures = append(existing.inventoryFailures, m.inventoryFailures...)
			for k, v := range m.current {
				existing.current[k] = v
			}
			continue
		}
		byStore[key] = m
		out = append(out, m)
	}
	return out
}

func fallbackStatPackages(ctx context.Context, env gotool.Environment) ([]pkgMeta, error) {
	p, err := currentModulePackage(ctx, env)
	if err != nil {
		return nil, err
	}
	return []pkgMeta{p}, nil
}

func currentModulePackage(ctx context.Context, env gotool.Environment) (pkgMeta, error) {
	out, err := gotool.List(ctx, "", env, "-m", "-json")
	if err != nil {
		return pkgMeta{}, err
	}
	var mod struct {
		Path string
		Dir  string
	}
	if err := json.Unmarshal(out, &mod); err != nil {
		return pkgMeta{}, fmt.Errorf("stat: decode go list -m: %w", err)
	}
	if mod.Path == "" || mod.Dir == "" {
		return pkgMeta{}, fmt.Errorf("stat: current module unavailable")
	}
	var p pkgMeta
	p.Dir = mod.Dir
	p.Module.Path = mod.Path
	p.Module.Dir = mod.Dir
	return p, nil
}

func addStatInventory(m *statModule, bl baseline, label string) error {
	if err := addRefInventory(m, bl.baseRef, label); err != nil {
		return err
	}
	if bl.newRef == "" {
		recs, err := m.store.ListCandidates()
		if err != nil {
			return err
		}
		for _, r := range recs {
			// Shape-failing pew recordings are inventoried too (spec §10.1):
			// the per-side checks warn and tally them, so "everything stale
			// (format)" never reads as "nothing recorded". Unmarked files at
			// layout paths are foreign and stay ignored.
			parsed, ok, err := m.readSide("", r.PkgRel, r.Bench, r.Label)
			if err != nil && !errors.Is(err, store.ErrInvalidRecording) {
				return err
			}
			if ok && (store.IsPewMarked(parsed) || errors.Is(err, store.ErrInvalidRecording)) && r.Label == label {
				m.keys[statKey{pkgRel: r.PkgRel, bench: r.Bench, label: r.Label}] = true
			}
		}
		return nil
	}
	return addRefInventory(m, bl.newRef, label)
}

func addRefInventory(m *statModule, ref, label string) error {
	paths, err := m.repo.ListAt(ref, m.benchDir)
	if err != nil {
		return err
	}
	for _, path := range paths {
		r, ok := m.store.KeyFromPath(path)
		if !ok || r.Label != label {
			continue
		}
		// Shape-failing pew recordings are inventoried too (spec §10.1): the
		// per-side checks warn and tally them, so "everything stale (format)"
		// never reads as "nothing recorded". Unmarked files at layout paths
		// are foreign and stay ignored.
		recsSide, sideOK, err := m.readSide(ref, r.PkgRel, r.Bench, r.Label)
		if err != nil && !errors.Is(err, store.ErrInvalidRecording) {
			return err
		}
		if !sideOK || (!store.IsPewMarked(recsSide) && !errors.Is(err, store.ErrInvalidRecording)) {
			continue
		}
		m.keys[statKey{pkgRel: r.PkgRel, bench: r.Bench, label: r.Label}] = true
	}
	return nil
}

func sortedStatKeys(keys map[statKey]bool) []statKey {
	out := make([]statKey, 0, len(keys))
	for k := range keys {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].pkgRel != out[j].pkgRel {
			return out[i].pkgRel < out[j].pkgRel
		}
		if out[i].bench != out[j].bench {
			return out[i].bench < out[j].bench
		}
		return out[i].label < out[j].label
	})
	return out
}

// isDirty reports whether a recording was made on a dirty working tree (§5).
// `dirty` is a closed recording key, and admitRecording has already refused
// any recording whose rows disagree on a closed key before this runs, so the
// first row decides for the whole recording.
func isDirty(recs []*benchfmt.Result) bool {
	return len(recs) > 0 && recs[0].GetConfig(runpkg.KeyDirty.Name) == "true"
}

// readSide loads one side's recording for a benchmark. ref == "" reads the
// working-tree file; otherwise the committed blob at ref is read (spec §6.1).
// ok is false (nil error) when the benchmark is not recorded on that side.
func readSide(st *store.Store, repo *gitblob.Repo, ref, pkgRel, bench, label string) ([]*benchfmt.Result, bool, error) {
	if ref == "" {
		recs, err := st.Read(pkgRel, bench, label)
		if errors.Is(err, store.ErrNotRecorded) {
			return nil, false, nil
		}
		if errors.Is(err, store.ErrInvalidRecording) {
			return nil, true, err
		}
		if err != nil {
			return nil, false, err
		}
		return recs, true, nil
	}
	abs, err := st.Path(pkgRel, bench, label)
	if err != nil {
		return nil, false, err
	}
	content, ok, err := repo.ReadAt(ref, abs)
	if err != nil {
		return nil, false, err
	}
	if !ok {
		return nil, false, nil
	}
	recs, err := store.Parse(bytes.NewReader(content), ref+":"+filepath.Base(abs))
	if errors.Is(err, store.ErrInvalidRecording) {
		return nil, true, err
	}
	if err != nil {
		return nil, false, err
	}
	return recs, true, nil
}

func (m *statModule) readSide(ref, pkgRel, bench, label string) ([]*benchfmt.Result, bool, error) {
	if m.sides == nil {
		m.sides = make(map[statSideKey]statSide)
	}
	key := statSideKey{ref: ref, pkgRel: pkgRel, bench: bench, label: label}
	if side, ok := m.sides[key]; ok {
		return side.recs, side.ok, side.err
	}
	recs, ok, err := readSide(m.store, m.repo, ref, pkgRel, bench, label)
	adm := admitRecording(recs, ref == "")
	m.sides[key] = statSide{recs: recs, ok: ok, err: err, admitted: &adm}
	return recs, ok, err
}
