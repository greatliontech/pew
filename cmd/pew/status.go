package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/guard"
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
			if err := resolveVouches(); err != nil {
				return err
			}
			if explain && jsonOut {
				return fmt.Errorf("status: --explain and -json are mutually exclusive (the explanation is a human view)")
			}
			patterns := args
			if len(patterns) == 0 {
				patterns = []string{"./..."}
			}
			return runStatus(cmd.OutOrStdout(), benchDir, label, staleOnly, explain, jsonOut, patterns)
		},
	}
	cmd.Flags().StringVar(&benchDir, "bench-dir", "", "stored-recordings directory (default <module>/benchmarks); an explicit value applies to every package")
	cmd.Flags().StringVar(&label, "label", "", "variant label to check (spec §6); empty = the unlabeled recording")
	cmd.Flags().BoolVar(&staleOnly, "stale", false, "show only benchmarks that need re-running (non-valid)")
	cmd.Flags().BoolVar(&explain, "explain", false, "explain each non-valid verdict: recorded vs current guard/input values (spec §12)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit one JSON object per row (spec §12, --json)")
	cmd.Flags().StringArrayVar(&rawVouches, "vouch", nil, "dynamic-state vouch IMPORT-PATH:VARIABLE (repeatable): a version-pinned dependency variable accepted as stable after initialization; discharges exactly that variable's shared-dynamic-state downgrade, the load-bearing set recorded as pew-vouches (spec §12)")
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
func newEngineForPkg(p pkgMeta, env []string) (*gofresh.Engine, string, error) {
	return newEngineAt(p.Module.Dir, p.Dir, p.Name == "main", env)
}

func newEngineAt(moduleDir, pkgDir string, mainPkg bool, env []string) (*gofresh.Engine, string, error) {
	if err := resolveVouches(); err != nil {
		return nil, "", err
	}
	goflags, err := runpkg.EffectiveGoflags(moduleDir, env)
	if err != nil {
		return nil, "", err
	}
	pgo, err := runpkg.PGOInput(moduleDir, pkgDir, mainPkg, goflags)
	if err != nil {
		return nil, "", err
	}
	e, err := buildEngine(moduleDir, env, pgo)
	return e, pgo, err
}

// rawVouches collects the --vouch flag values; resolveVouches parses
// them once into dynamicStateVouches before the first engine builds.
var rawVouches []string

// dynamicStateVouches is the process-wide reviewed vouch set from the
// --vouch flags: one set for every engine a command builds, so run,
// status, and stat verdicts judge under the same acceptances.
var dynamicStateVouches []string

// resolveVouches parses the collected --vouch values; every command
// path that builds engines calls it before the first build.
func resolveVouches() error {
	if len(rawVouches) == 0 {
		dynamicStateVouches = nil
		return nil
	}
	identities, err := parseDynamicStateVouches(rawVouches)
	if err != nil {
		return err
	}
	dynamicStateVouches = identities
	return nil
}

// parseDynamicStateVouches parses "IMPORT-PATH:VARIABLE" pairs (a colon
// cannot appear in an import path, so a bare package is unrepresentable)
// into gofresh's canonical identities, refusing control or space
// characters and a variable that is not one Go identifier.
func parseDynamicStateVouches(entries []string) ([]string, error) {
	var identities []string
	seen := map[string]bool{}
	for _, entry := range entries {
		pkg, name, ok := strings.Cut(entry, ":")
		if !ok || pkg == "" || name == "" {
			return nil, fmt.Errorf("vouch %q is not IMPORT-PATH:VARIABLE", entry)
		}
		for _, r := range pkg {
			if r <= ' ' || r == 0x7f || unicode.IsControl(r) {
				return nil, fmt.Errorf("vouch package %q carries a control or space character", pkg)
			}
		}
		for i, r := range name {
			letter := unicode.IsLetter(r) || r == '_'
			if (i == 0 && !letter) || (i > 0 && !letter && !unicode.IsDigit(r)) {
				return nil, fmt.Errorf("vouch variable %q is not one Go identifier", name)
			}
		}
		identity := pkg + "." + name
		if !seen[identity] {
			seen[identity] = true
			identities = append(identities, identity)
		}
	}
	sort.Strings(identities)
	return identities, nil
}

// engineDiagnostics receives payload-bearing gofresh diagnostics from
// every engine a command builds — command-wide configuration like the
// vouch set (a var, not a threaded parameter). The read is
// unsynchronized on analysis goroutines: tests may swap it only while
// no engine is live, and only engine-free tests do.
var engineDiagnostics io.Writer = os.Stderr

// emitEngineDiagnostic writes a payload-bearing gofresh event
// (per-subject analysis-unavailable provenance, the unlisted-toolchain
// notice) to the operator's log; detail-free keep-alives stay silent.
// Without a consumer, an unlisted release surfaces only as scattered
// stale/unverifiable verdicts with nothing naming the walk needed.
func emitEngineDiagnostic(p gofresh.Progress) {
	if p.Detail != "" {
		fmt.Fprintf(engineDiagnostics, "gofresh: %s %s — %s\n", p.Phase, p.Package, p.Detail)
	}
}

func buildEngine(moduleDir string, env []string, pgo string) (*gofresh.Engine, error) {
	// Every pew engine attests single-subject execution: `pew run`
	// measures each benchmark in a process of its own (spec §9), and
	// status/stat must judge recordings under the same premise they
	// were produced under — the attestation arms gofresh's audited
	// pooling discharge and rides the fact identity, so a split here
	// would make verdict surfaces disagree with the producer.
	if err := checkToolchainProvenance(moduleDir, env); err != nil {
		return nil, err
	}
	opts := []gofresh.Option{gofresh.WithDir(moduleDir), gofresh.WithEnv(env...), gofresh.WithSingleSubjectExecution(),
		gofresh.WithProgress(emitEngineDiagnostic)}
	if pgo != "" {
		opts = append(opts, gofresh.WithBuildInputs(pgo))
	}
	if len(dynamicStateVouches) > 0 {
		opts = append(opts, gofresh.WithDynamicStateVouches(dynamicStateVouches...))
	}
	return gofresh.New(opts...)
}

func runStatus(w io.Writer, benchDir, label string, staleOnly, explain, jsonOut bool, patterns []string) error {
	pkgs, err := resolvePackages(patterns)
	if err != nil {
		return err
	}
	for _, p := range pkgs {
		if p.Module.Dir == "" {
			continue // not in a module (e.g. a stdlib pattern) — nothing to record
		}
		// A per-package failure (an unreadable PGO profile, a sibling that
		// does not compile) is reported as a row and does not abort status of
		// the rest of the tree.
		reportErr := func(err error) {
			if jsonOut {
				_ = writeJSONLine(w, statusJSONRow{Package: p.ImportPath, Error: err.Error()})
				return
			}
			fmt.Fprintf(w, "%-12s %s  (%v)\n", "error", p.ImportPath, err)
		}
		e, _, err := newEngineForPkg(p, os.Environ())
		if err != nil {
			var pe *toolchainProvenanceError
			if errors.As(err, &pe) {
				return err
			}
			reportErr(err)
			continue
		}
		if err := statusPackage(w, os.Stderr, e, benchDir, label, staleOnly, explain, jsonOut, p); err != nil {
			reportErr(err)
		}
	}
	return nil
}

// warnForeignKeys surfaces read-time foreign-key detection on every
// verdict read (spec §5's read arm): the key fragments comparison
// grouping silently, and regeneration is the remediation.
func warnForeignKeys(errw io.Writer, pkgPath, bench string, keys []string) {
	for _, key := range keys {
		fmt.Fprintf(errw, "pew: warning: %s.%s recording carries foreign configuration key %q - written before the closed-set enforcement or hand-edited; it fragments comparison grouping silently, regenerate to clear (spec §5)\n", pkgPath, bench, key)
	}
}

func statusPackage(w, errw io.Writer, e *gofresh.Engine, benchDir, label string, staleOnly bool, explain, jsonOut bool, p pkgMeta) error {
	benches, err := selectedBenchmarks(p)
	if err != nil {
		return err
	}
	if len(benches) == 0 {
		return nil
	}
	dir := benchDir
	if dir == "" {
		dir = filepath.Join(p.Module.Dir, "benchmarks")
	}
	st := store.New(dir)
	pkgRel := strings.TrimPrefix(strings.TrimPrefix(p.ImportPath, p.Module.Path), "/")
	rows, err := checkPackage(st, e, p.ImportPath, pkgRel, p.Module.Dir, benches, label)
	if err != nil {
		return err
	}
	for _, b := range benches {
		bv := rows[b]
		v, reason, fp := bv.v, bv.reason, bv.fp
		warnForeignKeys(errw, p.ImportPath, b, bv.foreign)
		if staleOnly && v == verdictValid {
			continue
		}
		if jsonOut {
			if err := writeJSONLine(w, statusJSONRow{Package: p.ImportPath, Benchmark: b, Label: label, Verdict: string(v), Reason: reason}); err != nil {
				return err
			}
			continue
		}
		name := b
		if label != "" {
			// The row names the recording it inventoried: the labeled variant
			// carries its label exactly as its filename does.
			name = b + "." + label
		}
		line := fmt.Sprintf("%-12s %s.%s", v, p.ImportPath, name)
		if reason != "" {
			line += "  (" + reason + ")"
		}
		fmt.Fprintln(w, line)
		// fp.MaximalClosure is non-empty iff the format check passed: shape
		// validation requires a pew-closure key, so the empty sentinel is
		// exactly the unrecorded/stale-format/error set, which has nothing
		// decodable to tabulate.
		if explain && v != verdictValid && v != verdictUnrecorded && fp.MaximalClosure != "" {
			explainRecordAgainstCurrent(w, e, p.Module.Dir, p.ImportPath, b, fp, os.Environ())
		}
	}
	return nil
}

// checkOne is the per-benchmark validity verdict for status and run --stale
// (stat's working-tree staleness warning shares verdictForRecs below over its
// already-loaded rows). The engine recomputes the current
// closure and guards (the SSA load is the dominant cost; an unrecorded benchmark
// needs no analysis, so the store is read first).
// The fourth result is the encoded current compartment ledger, non-empty
// exactly when the verdict was granted through the inert-growth rule
// (spec §7.9): the returned fingerprint then carries the refreshed
// compartment pin, and the run path rewrites the recording so later
// verdicts read plainly valid.
func checkOne(st *store.Store, e *gofresh.Engine, pkgPath, pkgRel, moduleDir, bench, label string) (verdict, string, gofresh.Fingerprint, string, error) {
	recs, err := st.Read(pkgRel, bench, label)
	switch {
	case errors.Is(err, store.ErrNotRecorded):
		return verdictUnrecorded, "", gofresh.Fingerprint{}, "", nil
	case err != nil:
		return "", "", gofresh.Fingerprint{}, "", err
	}
	return verdictForRecs(e, pkgPath, moduleDir, bench, recs)
}

// benchVerdict is one recorded benchmark's package-batch verdict row:
// the verdict and its reason, the fingerprint the verdict was decided
// over, the encoded current ledger when the inert-growth rule granted
// it (spec §7.9), and any foreign configuration keys the stored
// recording carries (read-time trust detection, spec §5).
type benchVerdict struct {
	v           verdict
	reason      string
	fp          gofresh.Fingerprint
	grownLedger string
	foreign     []string
}

// checkPackage is the per-package batch form of checkOne: one analysis
// view serves every recorded benchmark's verdict (CheckBatch) and every
// inert-growth rider's ledger read, capture, and re-check. A package
// with N recorded benchmarks previously paid one view per benchmark
// plus one more per rider - cost, not soundness: the engine holds no
// cross-view cache, so the verdicts are identical either way. The
// per-benchmark purity fold and every per-recording gate are unchanged.
func checkPackage(st *store.Store, e *gofresh.Engine, pkgPath, pkgRel, moduleDir string, benches []string, label string) (map[string]*benchVerdict, error) {
	out := map[string]*benchVerdict{}
	type pending struct {
		bench  string
		fp     gofresh.Fingerprint
		pure   string
		ledger string
	}
	var checks []pending
	for _, b := range benches {
		recs, err := st.Read(pkgRel, b, label)
		switch {
		case errors.Is(err, store.ErrNotRecorded):
			out[b] = &benchVerdict{v: verdictUnrecorded}
			continue
		case err != nil:
			return nil, err
		}
		bv := &benchVerdict{foreign: store.ForeignConfigKeys(recs)}
		out[b] = bv
		if !store.IsRecordingShape(recs) {
			bv.v, bv.reason = verdictStale, "format"
			continue
		}
		fp, pure, recordedLedger, ok := fingerprintFromConfig(recs[0].Config)
		if !ok {
			bv.v, bv.reason = verdictStale, "format"
			continue
		}
		checks = append(checks, pending{b, fp, pure, recordedLedger})
	}
	if len(checks) == 0 {
		return out, nil
	}
	ctx := context.Background()
	subjects := make([]gofresh.Subject, 0, len(checks))
	recorded := map[gofresh.Subject]gofresh.Fingerprint{}
	for _, c := range checks {
		s := gofresh.Subject{Package: pkgPath, Symbol: c.bench}
		subjects = append(subjects, s)
		recorded[s] = c.fp
	}
	view, err := e.NewViewFor(ctx, subjects, moduleDir, gofresh.Measurement)
	if err != nil {
		return nil, err
	}
	verdicts, err := view.CheckBatch(ctx, recorded)
	if err != nil {
		return nil, err
	}
	for _, c := range checks {
		subject := gofresh.Subject{Package: pkgPath, Symbol: c.bench}
		bv := out[c.bench]
		bv.fp = c.fp
		v := verdicts[subject]
		pendingLedger := ""
		var refreshedFP gofresh.Fingerprint
		if v.Status == gofresh.Stale && v.Reason == "test variants" {
			if refreshed, encoded, rv, ok := inertGrownRecheckOn(view, subject, c.ledger, c.fp); ok {
				v, refreshedFP, pendingLedger = rv, refreshed, encoded
			}
		}
		v = applyPurity(v, c.pure)
		if v.Status == gofresh.Valid && pendingLedger != "" {
			bv.grownLedger = pendingLedger
			bv.fp = refreshedFP
		}
		bv.v, bv.reason = verdict(v.Status), v.Reason
	}
	return out, nil
}

// verdictForRecs is the verdict core over already-loaded recording rows:
// every verdict surface — status and run --stale through checkOne, stat's
// working-tree staleness warning directly — shares it, the inert-growth
// rule included (spec §7.9).
func verdictForRecs(e *gofresh.Engine, pkgPath, moduleDir, bench string, recs []*benchfmt.Result) (verdict, string, gofresh.Fingerprint, string, error) {
	if !store.IsRecordingShape(recs) {
		return verdictStale, "format", gofresh.Fingerprint{}, "", nil
	}
	fp, pure, recordedLedger, ok := fingerprintFromConfig(recs[0].Config)
	if !ok {
		return verdictStale, "format", gofresh.Fingerprint{}, "", nil
	}
	v, err := e.Check(context.Background(), fp, gofresh.Subject{Package: pkgPath, Symbol: bench}, moduleDir)
	if err != nil {
		return "", "", gofresh.Fingerprint{}, "", err
	}
	pendingLedger := ""
	var refreshedFP gofresh.Fingerprint
	if v.Status == gofresh.Stale && v.Reason == "test variants" {
		if refreshed, encoded, rv, ok := inertGrownRecheck(e, moduleDir, pkgPath, bench, recordedLedger, fp); ok {
			// The refreshed verdict takes the ordinary verdict's place and
			// rides the same purity fold below: a record that would read
			// valid but for the proven-inert compartment movement serves,
			// while a pin that hid behind the compartment reason surfaces
			// under its own attribution (spec §7.9).
			v, refreshedFP, pendingLedger = rv, refreshed, encoded
		}
	}
	v = applyPurity(v, pure)
	grownLedger := ""
	if v.Status == gofresh.Valid && pendingLedger != "" {
		// The serve succeeded: the refreshed fingerprint is the one the
		// verdict granted over, and the run path records it. A refused
		// re-check keeps returning the recorded fingerprint, so an
		// explanation lays the recording's own values against the current
		// tree — the moved pin and the moved compartment both surface.
		grownLedger = pendingLedger
		fp = refreshedFP
	}
	// The fingerprint the verdict was decided over rides back so an
	// explanation view describes the same recording — never a re-read that a
	// concurrent run could have replaced.
	return verdict(v.Status), v.Reason, fp, grownLedger, nil
}

// inertGrownRecheck applies the inert-growth verdict rule (spec §7.9) to a
// recording refused as exactly stale "test variants". That verdict
// certifies the benchmark's own source closure unchanged and nothing more —
// gofresh orders the compartment comparison after the core and before the
// environment tiers, so a moved guard or runtime input can hide behind that
// reason — so the rule completes the proof itself: the recorded compartment
// ledger must diff inert against the current view's (the only movement is
// added declarations no unchanged declaration can observe), and the
// recorded fingerprint refreshed to the current compartment hash re-checks,
// its verdict replacing the ordinary one — every remaining pin enforced
// exactly as an ordinary verdict, the purity fold included downstream. Any
// fault refuses and the original verdict stands.
func inertGrownRecheck(e *gofresh.Engine, moduleDir, pkgPath, bench, recordedLedger string, fp gofresh.Fingerprint) (gofresh.Fingerprint, string, gofresh.Verdict, bool) {
	subject := gofresh.Subject{Package: pkgPath, Symbol: bench}
	view, err := e.NewViewFor(context.Background(), []gofresh.Subject{subject}, moduleDir, gofresh.Measurement)
	if err != nil {
		return fp, "", gofresh.Verdict{}, false
	}
	return inertGrownRecheckOn(view, subject, recordedLedger, fp)
}

// inertGrownRecheckOn is the rule against a caller-supplied view - the
// package-batch path shares one view across every rider; the
// single-benchmark path (stat's working-tree warning) wraps it above.
func inertGrownRecheckOn(view *gofresh.View, subject gofresh.Subject, recordedLedger string, fp gofresh.Fingerprint) (gofresh.Fingerprint, string, gofresh.Verdict, bool) {
	if recordedLedger == "" {
		return fp, "", gofresh.Verdict{}, false
	}
	recorded, err := runpkg.DecodeLedger(recordedLedger)
	if err != nil {
		return fp, "", gofresh.Verdict{}, false
	}
	ctx := context.Background()
	current, err := view.TestVariantLedger(subject)
	if err != nil {
		return fp, "", gofresh.Verdict{}, false
	}
	if !gofresh.DiffTestVariantLedgers(recorded.ToGofresh(), current).Inert() {
		return fp, "", gofresh.Verdict{}, false
	}
	captured, err := view.Capture(ctx, subject)
	if err != nil || captured.TestVariantClosure == "" {
		return fp, "", gofresh.Verdict{}, false
	}
	refreshed := fp
	refreshed.TestVariantClosure = captured.TestVariantClosure
	v, err := view.Check(ctx, refreshed, subject)
	if err != nil {
		return fp, "", gofresh.Verdict{}, false
	}
	encoded, err := runpkg.EncodeLedger(runpkg.LedgerFromGofresh(current))
	if err != nil {
		return fp, "", gofresh.Verdict{}, false
	}
	return refreshed, encoded, v, true
}

// fingerprintFromConfig reads the recorded fingerprint out of a recording's config
// lines (spec §5: pew owns the serialization, gofresh owns the semantics), plus the
// recorded per-benchmark purity flag ("" when none).
func fingerprintFromConfig(cfg []benchfmt.Config) (gofresh.Fingerprint, string, string, bool) {
	m := make(map[string]string, len(cfg))
	formatCount := 0
	for _, c := range cfg {
		m[c.Key] = string(c.Value)
		if c.Key == "pew-format" {
			formatCount++
		}
	}
	if m["pew-format-invalid"] == "true" || formatCount != 1 || m["pew-format"] != runpkg.RecordingFormat {
		return gofresh.Fingerprint{}, "", "", false
	}
	return gofresh.Fingerprint{
		MaximalClosure:     m["pew-closure"],
		TestVariantClosure: m["pew-test-variants"],
		Guards: guard.Guards{
			Toolchain:     m["toolchain"],
			BuildConfig:   m["buildconfig"],
			Machine:       m["machine"],
			RuntimeConfig: m["runtimeconfig"],
		},
		PurityAssertion:          m["pew-purity"],
		DynamicStateVouches:      m["pew-vouches"],
		SingleSubjectDischarges:  m["pew-single-subject-discharges"],
		PackageProcessDischarges: m["pew-package-process-discharges"],
		DynamicStateStrategy:     m["pew-dynamic-state"],
		RuntimeInputs:            m["pew-runtime-inputs"],
		RuntimeDigest:            m["pew-runtime"],
		ResultKind:               gofresh.Measurement,
	}, m["pure"], m["pew-test-variant-ledger"], true
}

// applyPurity folds the recorded per-benchmark purity flag into the engine verdict
// (spec §7.3, §7.5). --impure (pure:false) declares external state: the benchmark
// always re-runs unless a guard already staled it, so any non-stale verdict becomes
// unverifiable "impure". --assume-pure (pure:true) is the author suppressing the
// remaining unverifiability after every hashable guard held, so unverifiable
// becomes valid — except an engine verdict carrying the //gofresh:external
// directive's reason: the in-code external declaration is not a blind spot the
// caller may vouch away (§7.5), exactly as the in-code //gofresh:pure channel is
// applied inside the engine itself (newEngineForPkg).
func applyPurity(v gofresh.Verdict, pure string) gofresh.Verdict {
	switch pure {
	case "false":
		if v.Status != gofresh.Stale {
			return gofresh.Verdict{Status: gofresh.Unverifiable, Reason: "impure"}
		}
	case "true":
		if v.Status == gofresh.Unverifiable && v.Reason != "external directive" {
			return gofresh.Verdict{Status: gofresh.Valid}
		}
	}
	return v
}

func resolvePackages(patterns []string) ([]pkgMeta, error) {
	out, err := gotool.Run(append([]string{"list", "-json"}, patterns...)...)
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
