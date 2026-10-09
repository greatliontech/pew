package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"

	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/pew/internal/gotool"
	runpkg "github.com/greatliontech/pew/internal/run"
	"github.com/greatliontech/pew/internal/store"
	"github.com/spf13/cobra"
	"golang.org/x/perf/benchfmt"
)

// verdict is a benchmark's status row: gofresh's freshness verdict plus the
// store-level "unrecorded".
type verdict string

const (
	verdictValid        = verdict(gofresh.Valid)
	verdictStale        = verdict(gofresh.Stale)
	verdictUnverifiable = verdict(gofresh.Unverifiable)
	verdictUnrecorded   = verdict("unrecorded")
)

func newStatusCmd() *cobra.Command {
	var rawVouches []string
	var benchDir string
	var label string
	var staleOnly bool
	var explain bool
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "status [packages]",
		Short: guidanceShort("status"),
		Long:  guidanceHelp("status"),
		RunE: func(cmd *cobra.Command, args []string) error {
			if explain && jsonOut {
				return fmt.Errorf("status: --explain and -json are mutually exclusive (the explanation is a human view)")
			}
			if err := store.ValidateLabel(label); err != nil {
				return err
			}
			patterns := args
			if len(patterns) == 0 {
				patterns = []string{"./..."}
			}
			ctx, stop := commandContext(cmd)
			defer stop()
			ctx, _, err := beginInvocation(ctx, benchDir, rawVouches, cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			return runStatus(ctx, cmd.OutOrStdout(), benchDir, label, staleOnly, explain, jsonOut, patterns)
		},
	}
	cmd.Flags().StringVar(&benchDir, "bench-dir", "", "")
	cmd.Flags().StringVar(&label, "label", "", "")
	cmd.Flags().BoolVar(&staleOnly, "stale", false, "")
	cmd.Flags().BoolVar(&explain, "explain", false, "")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "")
	cmd.Flags().StringArrayVar(&rawVouches, "vouch", nil, "")
	return cmd
}

type pkgMeta struct {
	ImportPath   string
	Name         string
	Dir          string
	TestGoFiles  []string
	XTestGoFiles []string
	Module       struct {
		Path string
		Dir  string
	}
}

// newEngineForPkg roots one immutable Gofresh configuration at a package's
// module. Views discover purity directives from their own selected source. The
// package's effective PGO profile — explicit -pgo in the effective GOFLAGS, or
// a tested main package's default.pgo — rides in as a content-digest build
// input, so the buildconfig guard moves when the profile's bytes do, not
// merely when the flag string does (spec §5/§9); a profile the compile will
// consume but pew cannot read fails closed here. The resolved input is
// returned beside the engine so the producer can revalidate it before writing.
func newEngineForPkg(ctx context.Context, p pkgMeta, env gotool.Environment) (*gofresh.Engine, string, error) {
	return newEngineAt(ctx, p.Module.Dir, p.Dir, p.Name == "main", env)
}

// newEngineForPkgProducer is newEngineForPkg with the environment the
// measured processes run under when it differs from the analysis one
// (a pinned run's GOMAXPROCS): the runtime-configuration guard and
// runtime-input revalidation take it, loads and builds keep env.
func newEngineForPkgProducer(ctx context.Context, p pkgMeta, env gotool.Environment, producerEnv *gotool.Environment) (*gofresh.Engine, string, error) {
	return newEngineAtProducer(ctx, p.Module.Dir, p.Dir, p.Name == "main", env, producerEnv)
}

func newEngineAt(ctx context.Context, moduleDir, pkgDir string, mainPkg bool, env gotool.Environment) (*gofresh.Engine, string, error) {
	return newEngineAtProducer(ctx, moduleDir, pkgDir, mainPkg, env, nil)
}

func newEngineAtProducer(ctx context.Context, moduleDir, pkgDir string, mainPkg bool, env gotool.Environment, producerEnv *gotool.Environment) (*gofresh.Engine, string, error) {
	reader, err := preparationReader(ctx, moduleDir, env)
	if err != nil {
		return nil, "", err
	}
	goflags, err := runpkg.EffectiveGoflags(ctx, reader)
	if err != nil {
		return nil, "", err
	}
	pgo, err := runpkg.PGOInput(moduleDir, pkgDir, mainPkg, goflags)
	if err != nil {
		return nil, "", err
	}
	e, err := buildEngine(ctx, moduleDir, env, producerEnv, pgo)
	return e, pgo, err
}

// emitEngineDiagnostic hands a payload-bearing gofresh event
// (per-subject analysis-unavailable provenance, the unlisted-toolchain
// notice) to the diagnostics sink, and a keep-alive that opens a unit
// of analysis work — gofresh's own per-unit phase set, never spelled
// here — to the reporter as the stretch in flight: a typed load's
// phases are the longest silent stretches a verb has (spec
// REQ-pew-progress); a unit event may carry no package (the observe
// and runtime passes), so the text is built without a doubled space.
// A keep-alive that is a fact — the served class — names no stretch.
// Without the payload consumer, an
// unlisted release surfaces only as scattered stale/unverifiable
// verdicts with nothing naming the walk needed.
func emitEngineDiagnostic(ctx context.Context, p gofresh.Progress) {
	if _, diagnostic := p.Diagnostic(); diagnostic {
		if i, ok := ctx.Value(invocationKey{}).(*invocation); ok && i.diagnostics != nil {
			i.diagnostics(p)
		} else {
			gofresh.DiagnosticsTo(os.Stderr)(p)
		}
		return
	}
	if p.IsUnit() {
		reportPhase(ctx, strings.TrimSpace("analysis "+p.Phase+" "+p.Package))
	}
}

func buildEngine(ctx context.Context, moduleDir string, env gotool.Environment, producerEnv *gotool.Environment, pgo string) (*gofresh.Engine, error) {
	// Every pew engine attests single-subject execution: `pew run`
	// measures each benchmark in a process of its own (spec §9), and
	// status/stat must judge recordings under the same premise they
	// were produced under — the attestation arms gofresh's audited
	// pooling discharge and rides the fact identity, so a split here
	// would make verdict surfaces disagree with the producer.
	checkedToolchain, err := checkToolchainProvenance(ctx, moduleDir, env)
	if err != nil {
		return nil, err
	}
	reportPhase(ctx, "preparing analysis under "+checkedToolchain)
	// The reviewed vouch set has one home, the store's root
	// (REQ-pew-vouch-source): the engine declines the module's own
	// vouches file and judges under the store's set extended by this
	// invocation's flags.
	opts := []gofresh.Option{gofresh.WithDir(moduleDir), gofresh.WithEnv(env.Values()...), gofresh.WithSingleSubjectExecution(),
		gofresh.WithDeferredCheckClose(),
		gofresh.WithProgress(func(p gofresh.Progress) { emitEngineDiagnostic(ctx, p) }), gofresh.WithoutRepositoryVouches()}
	if pgo != "" {
		opts = append(opts, gofresh.WithBuildInputs(pgo))
	}
	if producerEnv != nil {
		opts = append(opts, gofresh.WithProducerEnv(producerEnv.Values()...))
	}
	vouches, err := invocationVouches(ctx, moduleDir)
	if err != nil {
		return nil, err
	}
	if len(vouches) > 0 {
		opts = append(opts, gofresh.WithDynamicStateVouches(vouches...))
	}
	return gofresh.New(opts...)
}

func runStatus(ctx context.Context, w io.Writer, benchDir, label string, staleOnly, explain, jsonOut bool, patterns []string) error {
	inv, ok := ctx.Value(invocationKey{}).(*invocation)
	if !ok {
		var err error
		ctx, inv, err = beginInvocation(ctx, benchDir, nil, os.Stderr)
		if err != nil {
			return err
		}
	}
	env := inv.env
	benchDir = inv.vouches.storeDir
	reportPhase(ctx, "listing")
	pkgs, err := resolvePackages(ctx, env, patterns)
	if err != nil {
		if cancelledBy(ctx, err) {
			return interrupted("status: interrupted while listing packages")
		}
		return err
	}
	prepared := make([]struct {
		benches []string
		err     error
	}, len(pkgs))
	// Establish toolchain compatibility before emitting any verdict. Recording
	// inventory can then remain available when ordinary engine preparation fails.
	for i, p := range pkgs {
		if p.Module.Dir == "" {
			continue
		}
		if ctx.Err() != nil {
			return interrupted("status: interrupted before %s (package %d/%d)", p.ImportPath, i+1, len(pkgs))
		}
		prepared[i].benches, prepared[i].err = declaredBenchmarks(p)
		if prepared[i].err != nil || len(prepared[i].benches) == 0 {
			continue
		}
		reportPhase(ctx, fmt.Sprintf("judging %s (%d/%d)", p.ImportPath, i+1, len(pkgs)))
		_, err := checkToolchainProvenance(ctx, p.Module.Dir, env)
		if ctx.Err() != nil {
			return interrupted("status: interrupted while judging %s (package %d/%d)", p.ImportPath, i+1, len(pkgs))
		}
		if err != nil {
			return err
		}
	}
	var failures []error
	for i, p := range pkgs {
		if p.Module.Dir == "" {
			continue // not in a module (e.g. a stdlib pattern) — nothing to record
		}
		if err := ctx.Err(); err != nil {
			return interrupted("status: interrupted before %s (package %d/%d)", p.ImportPath, i+1, len(pkgs))
		}
		reportPhase(ctx, fmt.Sprintf("judging %s (%d/%d)", p.ImportPath, i+1, len(pkgs)))
		// A per-package failure (an unreadable PGO profile, a sibling that
		// does not compile) is reported as a row and does not abort status of
		// the rest of the tree.
		reportErr := func(err error) error {
			failures = append(failures, fmt.Errorf("%s: %w", p.ImportPath, err))
			if jsonOut {
				return writeJSONLine(w, statusJSONRow{Package: p.ImportPath, Error: err.Error()})
			}
			_, writeErr := fmt.Fprintf(w, "%-12s %s  (%v)\n", "error", p.ImportPath, err)
			return writeErr
		}
		// The declarations first: a package with no benchmark builds no
		// engine (two `go env` processes and the PGO digest it would
		// otherwise pay for nothing).
		benches, err := prepared[i].benches, prepared[i].err
		if err != nil {
			if writeErr := reportErr(err); writeErr != nil {
				return writeErr
			}
			continue
		}
		if len(benches) == 0 {
			continue
		}
		judging := func() error {
			return interrupted("status: interrupted while judging %s (package %d/%d)", p.ImportPath, i+1, len(pkgs))
		}
		getEngine := sync.OnceValues(func() (*gofresh.Engine, error) {
			e, _, err := newEngineForPkg(ctx, p, env)
			return e, err
		})
		if err := statusPackage(ctx, w, inv.errw, getEngine, benchDir, label, staleOnly, explain, jsonOut, p, benches, env); err != nil {
			if cancelledBy(ctx, err) {
				return judging()
			}
			var reported *reportedStatusError
			if errors.As(err, &reported) {
				failures = append(failures, err)
			} else if writeErr := reportErr(err); writeErr != nil {
				return writeErr
			}
		}
	}
	if err := interruptedAfterLastUnit(ctx, "status: interrupted after the last package; every verdict shown stands"); err != nil {
		return err
	}
	if len(failures) != 0 {
		return fmt.Errorf("status: incomplete report: %w", errors.Join(failures...))
	}
	return nil
}

// reportedStatusError carries failures whose recording-specific rows were
// already emitted. The verb still fails without emitting duplicate package rows.
type reportedStatusError struct{ error }

func (e *reportedStatusError) Unwrap() error { return e.error }

// warnForeignKeys surfaces read-time foreign-key detection on every
// verdict read (spec §5's read arm): the key fragments comparison
// grouping silently, and regeneration is the remediation.
func warnForeignKeys(errw io.Writer, pkgPath, bench string, keys []string) {
	for _, key := range keys {
		fmt.Fprintf(errw, "pew: warning: %s.%s recording carries foreign configuration key %q - written before the closed-set enforcement or hand-edited; it fragments comparison grouping silently, regenerate to clear (spec §5)\n", pkgPath, bench, key)
	}
}

// declaredBenchmarks parses a package's benchmark declarations — the
// one rule deciding which packages status builds an engine for: none
// declared, no engine.
func declaredBenchmarks(p pkgMeta) ([]string, error) {
	return selectedBenchmarks(p)
}

func statusPackage(ctx context.Context, w, errw io.Writer, getEngine func() (*gofresh.Engine, error), benchDir, label string, staleOnly bool, explain, jsonOut bool, p pkgMeta, benches []string, env gotool.Environment) error {
	dir, err := moduleBenchDir(benchDir, p.Module.Dir)
	if err != nil {
		return err
	}
	st := store.New(dir)
	pkgRel := packageRel(p)
	rows, err := checkPackage(ctx, st, func(subjects []gofresh.Subject) (*gofresh.View, error) {
		e, err := getEngine()
		if err != nil {
			return nil, err
		}
		return newViewFor(e, ctx, subjects, p.Module.Dir, gofresh.Measurement)
	}, p.ImportPath, pkgRel, p.Module.Dir, benches, label, nil)
	if err != nil {
		return err
	}
	var failures []error
	for _, b := range benches {
		if err := ctx.Err(); err != nil {
			return err
		}
		bv := rows[b]
		name := b
		if label != "" {
			name += "." + label
		}
		profileRows, profileErr := profileStatuses(ctx, st, bv.admitted, bv.view, p.ImportPath, b)
		if profileErr != nil {
			failures = append(failures, fmt.Errorf("%s.%s profiles: %w", p.ImportPath, b, profileErr))
		}
		if bv.err != nil {
			failures = append(failures, fmt.Errorf("%s.%s: %w", p.ImportPath, b, bv.err))
			if jsonOut {
				if err := writeJSONLine(w, statusJSONRow{Package: p.ImportPath, Benchmark: b, Label: label, Error: bv.err.Error(), Profiles: profileRows}); err != nil {
					return err
				}
			} else if _, err := fmt.Fprintf(w, "%-12s %s.%s  (%v)\n", "error", p.ImportPath, name, bv.err); err != nil {
				return err
			}
			if !jsonOut {
				if err := renderProfiles(w, profileRows, explain); err != nil {
					return err
				}
			}
			continue
		}
		v, reason, fp := bv.v, bv.reason, bv.fp
		warnForeignKeys(errw, p.ImportPath, b, bv.foreign)
		if staleOnly && v == verdictValid && profileErr == nil {
			continue
		}
		if jsonOut {
			if err := writeJSONLine(w, statusJSONRow{Package: p.ImportPath, Benchmark: b, Label: label, Verdict: string(v), Reason: reason, Profiles: profileRows}); err != nil {
				return err
			}
			continue
		}
		line := fmt.Sprintf("%-12s %s.%s", v, p.ImportPath, name)
		if reason != "" {
			line += "  (" + reason + ")"
		}
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
		if err := renderProfiles(w, profileRows, explain); err != nil {
			return err
		}
		// fp.MaximalClosure is non-empty iff the recording decoded: the
		// format rung requires a pew-closure key, so the empty sentinel is
		// exactly the unrecorded/stale-format/error set, which has nothing
		// decodable to tabulate — a strategy-refused recording decoded and
		// explains like any stale one.
		if explain && v != verdictValid && v != verdictUnrecorded && fp.MaximalClosure != "" {
			e, err := getEngine()
			if err != nil {
				if _, writeErr := fmt.Fprintf(w, "    cannot compute the current state: %v\n", err); writeErr != nil {
					return writeErr
				}
				failures = append(failures, fmt.Errorf("%s.%s explanation: %w", p.ImportPath, b, err))
				continue
			}
			subject := gofresh.Subject{Package: p.ImportPath, Symbol: b}
			current := bv.current
			if current.MaximalClosure == "" {
				view, err := e.NewViewFor(ctx, []gofresh.Subject{subject}, p.Module.Dir, gofresh.Measurement)
				if err != nil {
					failures = append(failures, err)
					continue
				}
				current, err = view.Capture(ctx, subject)
				if err != nil {
					failures = append(failures, err)
					continue
				}
			}
			if err := explainCapturedRecord(ctx, w, p.Module.Dir, fp, current, env.Values()); err != nil {
				failures = append(failures, fmt.Errorf("%s.%s explanation: %w", p.ImportPath, b, err))
			}
		}
	}
	if len(failures) != 0 {
		return &reportedStatusError{errors.Join(failures...)}
	}
	return nil
}

// newViewFor builds through this operation's analysis dependency. The run's
// one view serves both freshness judgment and the siblings' captured facts.
func newViewFor(e *gofresh.Engine, ctx context.Context, subjects []gofresh.Subject, moduleDir string, kind gofresh.Kind) (*gofresh.View, error) {
	return dependencies(ctx).view(e, ctx, subjects, moduleDir, kind)
}

// benchVerdict is one recorded benchmark's package-batch verdict row:
// the verdict and its reason, the fingerprint the verdict was decided
// over, the encoded current ledger when the inert-growth rule granted
// it (spec §7.9), and any foreign configuration keys the stored
// recording carries (read-time trust detection, spec §5).
type benchVerdict struct {
	err         error
	v           verdict
	reason      string
	fp          gofresh.Fingerprint
	grownLedger string
	foreign     []string
	admitted    admission
	view        *gofresh.View
	current     gofresh.Fingerprint
}

// checkPackage reads store recordings before constructing one analysis
// view serving every admitted benchmark's observed verdict and every
// inert-growth rider's ledger read, capture, and re-check. A package
// with N recorded benchmarks previously paid one view per benchmark
// plus one more per rider - cost, not soundness: every fingerprint
// component is per subject or per package except the guards, which
// come from the module, environment, and kind alone, so the verdicts
// are identical under any subject grouping. Every per-recording gate
// is unchanged.
func checkPackage(ctx context.Context, st *store.Store, buildView func([]gofresh.Subject) (*gofresh.View, error), pkgPath, pkgRel, moduleDir string, benches []string, label string, view *gofresh.View) (map[string]*benchVerdict, error) {
	out := map[string]*benchVerdict{}
	for _, b := range benches {
		raw, err := st.ReadBytes(store.Key{PkgRel: pkgRel, Bench: b, Label: label})
		var recs []*benchfmt.Result
		if err == nil {
			recs, err = store.Parse(bytes.NewReader(raw), b)
		}
		switch {
		case errors.Is(err, store.ErrNotRecorded), errors.Is(err, os.ErrNotExist):
			out[b] = &benchVerdict{v: verdictUnrecorded}
			continue
		case errors.Is(err, store.ErrInvalidRecording):
			out[b] = &benchVerdict{v: verdictStale, reason: "format"}
			continue
		case err != nil:
			out[b] = &benchVerdict{err: err}
			continue
		}
		bv := &benchVerdict{foreign: store.ForeignConfigKeys(recs)}
		out[b] = bv
		adm := admitRecording(recs, true)
		adm.raw = raw
		bv.admitted = adm
		if !adm.ok {
			// A strategy-refused recording decoded: its fingerprint rides
			// the row so --explain can lay it against the current tree.
			bv.v, bv.reason, bv.fp = verdictStale, adm.class, adm.fp
			continue
		}
	}
	return judgeRecordings(ctx, buildView, pkgPath, benches, out, view)
}

// judgeRecordings is the current-tree verdict path for store and ref-paired
// callers alike. Admission and historical comparison never select this policy.
func judgeRecordings(ctx context.Context, buildView func([]gofresh.Subject) (*gofresh.View, error), pkgPath string, benches []string, out map[string]*benchVerdict, view *gofresh.View) (map[string]*benchVerdict, error) {
	type pending struct {
		bench  string
		fp     gofresh.Fingerprint
		ledger string
	}
	var checks []pending
	for _, b := range benches {
		bv := out[b]
		if bv.err == nil && bv.admitted.ok {
			checks = append(checks, pending{b, bv.admitted.fp, bv.admitted.ledger})
		}
	}
	if len(checks) == 0 {
		return out, nil
	}
	unjudged := func(err error) (map[string]*benchVerdict, error) {
		if cancelledBy(ctx, err) {
			return nil, err
		}
		for _, c := range checks {
			out[c.bench].err = err
		}
		return out, nil
	}
	subjects := make([]gofresh.Subject, 0, len(checks))
	recorded := map[gofresh.Subject]gofresh.Fingerprint{}
	for _, c := range checks {
		s := gofresh.Subject{Package: pkgPath, Symbol: c.bench}
		subjects = append(subjects, s)
		recorded[s] = c.fp
	}
	// The caller's view when it has one (the run's, over every selected
	// benchmark — the recorded ones are a subset). A view's subject set
	// changes no verdict: every fingerprint component is per subject or
	// per package except the guards, which are captured from the module,
	// environment, and kind alone, so a superset view judges exactly as
	// one built here over the recorded subjects would.
	if view == nil {
		built, err := buildView(subjects)
		if err != nil {
			return unjudged(err)
		}
		view = built
	} else {
		var err error
		view, err = view.Sibling(subjects)
		if err != nil {
			return unjudged(err)
		}
	}
	verdicts, err := view.CheckObservedBatch(ctx, recorded)
	if err != nil {
		return unjudged(err)
	}
	for _, c := range checks {
		subject := gofresh.Subject{Package: pkgPath, Symbol: c.bench}
		bv := out[c.bench]
		bv.view = view
		bv.fp = c.fp
		v := verdicts[subject]
		pendingLedger := ""
		var refreshedFP gofresh.Fingerprint
		if v.Status == gofresh.Stale && v.Reason == gofresh.ReasonTestVariants {
			if refreshed, encoded, rv, ok := inertGrownRecheckOn(ctx, view, subject, c.ledger, c.fp); ok {
				v, refreshedFP, pendingLedger = rv, refreshed, encoded
			}
		}
		if v.Status == gofresh.Valid && pendingLedger != "" {
			bv.grownLedger = pendingLedger
			bv.fp = refreshedFP
		}
		bv.v, bv.reason = verdict(v.Status), v.Reason
		bv.current, err = view.Capture(ctx, subject)
		if err != nil {
			return unjudged(err)
		}
	}
	if err := view.Validate(ctx); err != nil {
		return unjudged(err)
	}
	return out, nil
}

// inertGrownRecheckOn asks the shared engine to license applicability from
// the paired historical ledger. No producing field is rewritten by Pew.
func inertGrownRecheckOn(ctx context.Context, view *gofresh.View, subject gofresh.Subject, recordedLedger string, fp gofresh.Fingerprint) (gofresh.Fingerprint, string, gofresh.Verdict, bool) {
	if recordedLedger == "" {
		return fp, "", gofresh.Verdict{}, false
	}
	recorded, err := runpkg.DecodeLedger(recordedLedger)
	if err != nil {
		return fp, "", gofresh.Verdict{}, false
	}
	refreshed, current, v, err := view.CheckObservedInertTestVariantExtension(ctx, fp, recorded.ToGofresh(), subject)
	if err != nil {
		return fp, "", gofresh.Verdict{}, false
	}
	if v.Status != gofresh.Valid {
		return fp, "", v, true
	}
	encoded, err := runpkg.EncodeLedger(runpkg.LedgerFromGofresh(current))
	if err != nil {
		return fp, "", gofresh.Verdict{}, false
	}
	return refreshed, encoded, v, true
}

// fingerprintFromConfig reads the native payload after the format rung and
// returns Pew's companion ledger without changing historical evidence.
func fingerprintFromConfig(cfg []benchfmt.Config) (gofresh.Fingerprint, string, bool) {
	// The format rung is the store's one judgment; this reader keeps
	// only the fingerprint's restoration from the rows.
	if !store.FormatCurrent(cfg) {
		return gofresh.Fingerprint{}, "", false
	}
	m := make(map[string]string, len(cfg))
	for _, c := range cfg {
		m[c.Key] = string(c.Value)
	}
	fp, err := runpkg.RecordedFingerprint(cfg)
	if err != nil {
		return gofresh.Fingerprint{}, "", false
	}
	return fp, m[runpkg.KeyTestVariantLedger.Name], true
}

func resolvePackages(ctx context.Context, env gotool.Environment, patterns []string) ([]pkgMeta, error) {
	out, err := gotool.List(ctx, "", env, append([]string{"-json"}, patterns...)...)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(out))
	var pkgs []pkgMeta
	for dec.More() {
		var p pkgMeta
		if err := dec.Decode(&p); err != nil {
			return nil, fmt.Errorf("status: decode go list: %w", err)
		}
		pkgs = append(pkgs, p)
	}
	return pkgs, nil
}
