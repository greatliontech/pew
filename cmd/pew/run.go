package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/pew/internal/gitblob"
	"github.com/greatliontech/pew/internal/run"
	"github.com/greatliontech/pew/internal/store"
	"github.com/spf13/cobra"
	"golang.org/x/perf/benchfmt"
)

type runConfig struct {
	benchDir, label string
	// pin is the derived CPU set a pinned run measures on, unpinned when
	// empty; the --pin switch derives it at entry.
	pin         run.Pin
	opts        run.Options
	strict, all bool
	// throttle snapshots the thermal-throttle counters bracketing each
	// benchmark's measurement invocation (spec §9); nil means
	// run.SnapshotThrottle. A seam so tests control the observed delta
	// deterministically.
	throttle func() run.ThrottleSnapshot
	// execute runs one go-test invocation; nil means run.Execute. A seam so
	// tests can observe invocation order against the throttle bracket.
	execute func(moduleDir, pin string, env, args []string) ([]byte, error)
	// beforePersist runs after an arm measured and before its write gate
	// — the window between the arm's state bracket and its persist. A
	// seam so tests can move the tree there and pin the gate.
	beforePersist func(bench string)
}

func (rc runConfig) snapshotThrottle() run.ThrottleSnapshot {
	if rc.throttle != nil {
		return rc.throttle()
	}
	return run.SnapshotThrottle()
}

func (rc runConfig) executeGo(ctx context.Context, moduleDir, pin string, env, args []string) ([]byte, error) {
	if rc.execute != nil {
		return rc.execute(moduleDir, pin, env, args)
	}
	return run.ExecuteContext(ctx, moduleDir, pin, env, args)
}

func newRunCmd() *cobra.Command {
	var rc runConfig
	var pin bool
	rc.opts = run.Options{Count: 10, Benchtime: "1s", Bench: "."}
	cmd := &cobra.Command{
		Use:   "run [packages]",
		Short: guidanceShort("run"),
		Long:  guidanceHelp("run"),
		RunE: func(cmd *cobra.Command, args []string) error {
			// A label the store cannot name is a flag value: it refuses
			// here, before any listing or measurement (spec
			// REQ-pew-preparation).
			if err := store.ValidateLabel(rc.label); err != nil {
				return err
			}
			if err := validateBenchmarkPattern(rc.opts.Bench); err != nil {
				return err
			}
			vouchStoreDir = rc.benchDir
			if err := resolveVouches(); err != nil {
				return err
			}
			if pin {
				derived, err := derivePin(cmd.ErrOrStderr())
				if err != nil {
					return fmt.Errorf("run: %w", err)
				}
				rc.pin = derived
			}
			patterns := args
			if len(patterns) == 0 {
				patterns = []string{"./..."}
			}
			ctx, stop := commandContext(cmd)
			defer stop()
			return runRun(ctx, cmd.OutOrStdout(), cmd.ErrOrStderr(), rc, patterns)
		},
	}
	f := cmd.Flags()
	f.StringVar(&rc.benchDir, "bench-dir", "", "stored-recordings directory (default <module>/benchmarks)")
	f.IntVar(&rc.opts.Count, "count", 10, "-count: measurement runs per benchmark")
	f.StringVar(&rc.opts.Benchtime, "benchtime", "1s", "-benchtime: duration/iterations per measurement")
	f.StringVar(&rc.opts.Bench, "bench", ".", "-bench: benchmark name pattern")
	f.BoolVar(&pin, "pin", false, "pin the measurement to one CPU set derived from the host's topology (taskset)")
	f.BoolVar(&rc.strict, "strict", false, "treat quiesce warnings as fatal")
	f.StringVar(&rc.label, "label", "", "variant label for the recording filename")
	f.BoolVar(&rc.all, "all", false, "measure every selected benchmark, a valid recording included (the default serves what is proven and measures the rest)")
	f.StringArrayVar(&rawVouches, "vouch", nil, "dynamic-state vouch IMPORT-PATH:VARIABLE (repeatable): a version-pinned dependency variable accepted as stable after initialization; discharges exactly that variable's shared-dynamic-state downgrade, the load-bearing set recorded as pew-vouches (spec §12)")
	return cmd
}

func runRun(ctx context.Context, w, errw io.Writer, rc runConfig, patterns []string) error {
	reportPhase("listing")
	pkgs, err := resolvePackages(patterns)
	if err != nil {
		return err
	}
	// One pre-run observation both drives the quiesce gate and is recorded as
	// the pew-runconditions provenance line (spec §9), so the recording states
	// exactly the conditions the gate evaluated.
	conditions := run.ObserveConditions()
	if warns := conditions.Warnings(); len(warns) > 0 {
		for _, x := range warns {
			fmt.Fprintln(errw, "pew: warning:", x)
		}
		if rc.strict {
			return fmt.Errorf("run: refusing to run under noisy conditions (--strict)")
		}
	}
	var excludeDirs []string
	seenExclude := map[string]bool{}
	for _, p := range pkgs {
		if p.Module.Dir == "" {
			continue
		}
		benchDir, err := moduleBenchDir(rc.benchDir, p.Module.Dir)
		if err != nil {
			return err
		}
		if !seenExclude[benchDir] {
			seenExclude[benchDir] = true
			excludeDirs = append(excludeDirs, benchDir)
		}
	}
	gc := newGitStateCache(excludeDirs)
	envs := newEnvironments(os.Environ(), rc.pin)
	// Every package prepares before any package measures (spec
	// REQ-pew-preparation): the refusals the listing and the flags decide
	// — the benchmark declarations, the store destinations, the effective
	// GOFLAGS and PGO input, the toolchain provenance of every module —
	// fire here, so a later package's preparation failure is known
	// before an earlier package spends its measurement. Like status, a
	// per-package failure (one that does not build, an unreadable PGO
	// profile) is reported and does not abort the rest of the tree; a
	// toolchain provenance failure aborts the invocation.
	var failures []string
	var prepared []*packagePreparation
	for i, p := range pkgs {
		if p.Module.Dir == "" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return interrupted("interrupted while preparing %s (package %d/%d); nothing measured", p.ImportPath, i+1, len(pkgs))
		}
		reportPhase(fmt.Sprintf("preparing %s (%d/%d)", p.ImportPath, i+1, len(pkgs)))
		prep, err := preparePackage(rc, p, envs)
		if err != nil {
			var pe *toolchainProvenanceError
			if errors.As(err, &pe) {
				return err
			}
			fmt.Fprintf(w, "%-12s %s  (%v)\n", "error", p.ImportPath, err)
			failures = append(failures, p.ImportPath)
			// A refused package runs nothing, but the sweep still visits
			// it: its leftover would otherwise enter a sibling's baseline
			// and stamp that sibling's recording dirty.
			if prep != nil {
				prep.runBenches = nil
				prepared = append(prepared, prep)
			}
			continue
		}
		prepared = append(prepared, prep)
	}
	// The scratch sweep runs before the module state cache pins its
	// baseline: a leftover carrying git-visible files would otherwise
	// enter the cached baseline, and its removal would abort the run as
	// "repository state moved" — the exact failure the sweep exists to
	// prevent. It sweeps the prepared packages' directories with the
	// directives their records carry.
	for _, prep := range prepared {
		if err := sweepScratchLeftovers(errw, prep.pkg.Dir, prep.scratch); err != nil {
			return err
		}
	}
	for _, prep := range prepared {
		_, _ = gc.state(prep.pkg.Module.Dir)
	}
	for i, prep := range prepared {
		if len(prep.runBenches) == 0 {
			continue
		}
		if err := ctx.Err(); err != nil {
			return interrupted("interrupted before %s (package %d/%d)%s", prep.pkg.ImportPath, i+1, len(prepared), failedSoFar(failures))
		}
		if runErr := runPreparedPackage(ctx, w, errw, gc, rc, prep, envs, conditions); runErr != nil {
			var stopped *interruptedError
			if errors.As(runErr, &stopped) {
				// The run ends here with the interruption's own report:
				// every arm persisted so far is kept, nothing after it
				// starts, and the packages that failed before it are
				// still named.
				return interrupted("%s%s", runErr.Error(), failedSoFar(failures))
			}
			fmt.Fprintf(w, "%-12s %s  (%v)\n", "error", prep.pkg.ImportPath, runErr)
			failures = append(failures, prep.pkg.ImportPath)
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("run: %d package(s) failed: %s", len(failures), strings.Join(failures, ", "))
	}
	return nil
}

// gitStateCache pins each module's command-entry state. Later package runs can
// exclude recording writes made by earlier packages without changing the
// provenance recorded for the command.
type gitStateCache struct {
	entries map[string]gitStateResult
	exclude []string
}

type gitStateResult struct {
	state gitblob.RepositoryState
	err   error
}

// packageRel is a package's module-relative, slash-separated path — the
// store's package coordinate ("" for the module root).
func packageRel(p pkgMeta) string {
	return strings.TrimPrefix(strings.TrimPrefix(p.ImportPath, p.Module.Path), "/")
}

// moduleBenchDir resolves the recording store for a module: the configured
// bench-dir, or <module>/benchmarks, absolute and symlink-resolved — the
// one subtree the repository-state bracket excludes (spec §5). Resolution
// matters: the paths the exclusion must match are resolved (go list runs
// under the pinned resolved-PWD policy), while a relative --bench-dir made
// absolute through an alias cwd would silently never match. The store may
// not exist yet, so the nearest existing ancestor resolves and the tail
// rejoins.
func moduleBenchDir(configured, moduleDir string) (string, error) {
	dir := configured
	if dir == "" {
		dir = filepath.Join(moduleDir, "benchmarks")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	ancestor, tail := abs, ""
	for {
		resolved, err := filepath.EvalSymlinks(ancestor)
		if err == nil {
			return filepath.Join(resolved, tail), nil
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return abs, nil
		}
		tail = filepath.Join(filepath.Base(ancestor), tail)
		ancestor = parent
	}
}

// newGitStateCache pins repository state with the invocation-wide union of
// recording stores excluded: in a multi-module repository, one module's
// recordings must not taint a sibling module's provenance either
// (spec §5).
// rejectStoreCoveredSources enforces the exclusion's precondition: no
// measured source may live under the recording store, because the store's
// subtree is excluded from the worktree-state-drift guard wholesale — a
// closure file hiding there could move mid-run unseen, the false-valid
// direction (spec §5).
func rejectStoreCoveredSources(sourceFiles []string, benchDirs ...string) error {
	for _, src := range sourceFiles {
		abs, err := filepath.Abs(src)
		if err != nil {
			return err
		}
		if resolved, err := filepath.EvalSymlinks(abs); err == nil {
			abs = resolved
		}
		for _, benchDir := range benchDirs {
			rel, err := filepath.Rel(benchDir, abs)
			if err == nil && filepath.IsLocal(rel) {
				return fmt.Errorf("run: measured source %s lies under the recording store %s; the store is excluded from worktree-state tracking, so it must not contain measured source", src, benchDir)
			}
		}
	}
	return nil
}

func newGitStateCache(excludeDirs []string) *gitStateCache {
	return &gitStateCache{
		entries: map[string]gitStateResult{},
		exclude: excludeDirs,
	}
}

func (c *gitStateCache) state(moduleDir string) (gitblob.RepositoryState, error) {
	if r, ok := c.entries[moduleDir]; ok {
		return r.state, r.err
	}
	state, err := c.snapshot(moduleDir)
	c.entries[moduleDir] = gitStateResult{state: state, err: err}
	return state, err
}

// snapshot is the one Snapshot entry for the run path, carrying the
// invocation-wide store exclusion.
func (c *gitStateCache) snapshot(moduleDir string) (gitblob.RepositoryState, error) {
	return gitblob.Snapshot(moduleDir, c.exclude...)
}

// failedSoFar names the packages that failed before an interruption,
// for the interruption's own report.
func failedSoFar(failures []string) string {
	if len(failures) == 0 {
		return ""
	}
	return fmt.Sprintf("; %d package(s) failed before it: %s", len(failures), strings.Join(failures, ", "))
}

// packagePreparation is one package's preparation record (spec
// REQ-pew-preparation): everything the listing and the flags decide,
// computed after `go list` and before any engine build or measurement
// — the benchmark declarations and the ones the pattern selects, the
// scratch directives, the recording store and the validated
// destinations, and the engine with its effective PGO input (which
// samples the module's GOFLAGS and toolchain provenance). A package
// with nothing selected keeps a record too — its scratch directives
// still drive the entry sweep — but builds no engine and has nothing
// to refuse or run.
type packagePreparation struct {
	pkg                 pkgMeta
	benches, runBenches []string
	scratch             []string
	benchDir, pkgRel    string
	st                  *store.Store
	destinations        []store.Destination
	engine              *gofresh.Engine
	pgoInput            string
}

// preparePackage builds a package's preparation record, firing every
// refusal its inputs decide. The order is the cost order: the
// declarations (a parse) and the destinations (path rules and one
// Lstat each) before the engine (two `go env` processes and the PGO
// digest), so the cheapest refusal fires first. A refused package
// still returns the record it got as far as — its scratch directives
// drive the entry sweep whether or not it runs — beside the error.
func preparePackage(rc runConfig, p pkgMeta, envs environments) (*packagePreparation, error) {
	// A pinned run's engine judges and captures the runtime-configuration
	// guard under the measured process's environment (its producer
	// environment), while loads and builds stay on the analysis one.
	return preparePackageWith(rc, p, func() (*gofresh.Engine, string, error) {
		return newEngineForPkgProducer(p, envs.analysis, envs.runtime)
	})
}

// preparePackageWith is preparePackage over a caller-supplied engine
// constructor — the seam tests inject a prebuilt engine through.
func preparePackageWith(rc runConfig, p pkgMeta, newEngine func() (*gofresh.Engine, string, error)) (*packagePreparation, error) {
	scratch, err := scratchPatterns(p)
	if err != nil {
		return nil, err
	}
	benches, err := selectedBenchmarks(p)
	if err != nil {
		return nil, err
	}
	prep := &packagePreparation{pkg: p, benches: benches, scratch: scratch}
	if len(benches) == 0 {
		return prep, nil
	}
	runBenches, err := matchingBenchmarks(benches, rc.opts.Bench)
	if err != nil {
		return prep, err
	}
	if len(runBenches) == 0 {
		return prep, nil
	}
	dir, err := moduleBenchDir(rc.benchDir, p.Module.Dir)
	if err != nil {
		return prep, err
	}
	st := store.New(dir)
	pkgRel := packageRel(p)
	keys := make([]store.Key, 0, len(runBenches))
	for _, bench := range runBenches {
		keys = append(keys, store.Key{PkgRel: pkgRel, Bench: bench, Label: rc.label})
	}
	destinations, err := st.Destinations(keys)
	if err != nil {
		return prep, err
	}
	e, pgoInput, err := newEngine()
	if err != nil {
		return prep, err
	}
	prep.runBenches, prep.benchDir, prep.pkgRel, prep.st, prep.destinations, prep.engine, prep.pgoInput = runBenches, dir, pkgRel, st, destinations, e, pgoInput
	return prep, nil
}

func runPreparedPackage(ctx context.Context, w, errw io.Writer, gc *gitStateCache, rc runConfig, prep *packagePreparation, envs environments, conditions run.Conditions) error {
	env := envs.analysis
	p, e, st, pkgRel, scratch, runBenches := prep.pkg, prep.engine, prep.st, prep.pkgRel, prep.scratch, prep.runBenches
	baseline, err := gc.state(p.Module.Dir)
	if err != nil {
		return err
	}
	commit, initialDirty := baseline.Commit, baseline.Dirty

	opts := rc.opts
	// The package's one typed view, over every selected benchmark: the
	// freshness judgment reads it and the capture reads it — a second
	// load over the same subjects would only re-derive the first
	// (REQ-pew-serve-proven).
	reportPhase("loading " + p.ImportPath)
	subjects := make([]gofresh.Subject, 0, len(runBenches))
	for _, name := range runBenches {
		subjects = append(subjects, gofresh.Subject{Package: p.ImportPath, Symbol: name})
	}
	view, err := newViewFor(e, ctx, subjects, p.Module.Dir, gofresh.Measurement)
	if err != nil {
		return err
	}
	// The two refusals the view decides fire the moment it exists —
	// before the freshness read (which may rewrite an inert-growth
	// recording), the warm-up build, and the first arm (spec
	// REQ-pew-preparation): a measured source under the recording
	// store, or a recording destination overlapping a source input.
	if err := rejectStoreCoveredSources(view.SourceFiles(), gc.exclude...); err != nil {
		return err
	}
	recordingPaths := make([]string, 0, len(prep.destinations))
	for _, d := range prep.destinations {
		recordingPaths = append(recordingPaths, d.Path)
	}
	if err := rejectRecordingDestinations(view.SourceFiles(), recordingPaths); err != nil {
		return err
	}
	if !rc.all {
		// Serve what is proven, measure the rest: a benchmark whose
		// recording is valid against this view is not re-measured.
		need, err := nonValid(ctx, errw, st, e, p.ImportPath, pkgRel, p.Module.Dir, rc.label, runBenches, view)
		if err != nil {
			return err
		}
		// The served count is part of the default output: a run over
		// ten benchmarks that prints two `recorded` lines would
		// otherwise read as eight missing (spec §12).
		served := len(runBenches) - len(need)
		if len(need) == 0 {
			fmt.Fprintf(w, "%-12s %s: %d valid, nothing to run\n", "served", p.ImportPath, served)
			return nil
		}
		if served > 0 {
			fmt.Fprintf(w, "%-12s %s: %d valid, measuring %d\n", "served", p.ImportPath, served, len(need))
		}
		opts.Bench, err = restrictBenchmarkPattern(opts.Bench, need)
		if err != nil {
			return err
		}
		runBenches = need
		// Capture only what measures: a served benchmark's fingerprint
		// is never read (cost, not correctness).
		subjects = subjects[:0]
		for _, name := range runBenches {
			subjects = append(subjects, gofresh.Subject{Package: p.ImportPath, Symbol: name})
		}
	}
	startState, err := gc.snapshot(p.Module.Dir)
	if err != nil {
		return err
	}
	if !baseline.Equal(startState) {
		return fmt.Errorf("repository state moved before benchmark run")
	}
	fingerprints := make(map[string]gofresh.Fingerprint, len(subjects))
	for _, subject := range subjects {
		fp, err := view.Capture(ctx, subject)
		if err != nil {
			return err
		}
		fingerprints[subject.Symbol] = fp
	}
	// One compartment ledger covers the whole package: it derives from the
	// same view snapshot every fingerprint's compartment hash pinned, and
	// the inert-growth rule diffs it at verdict time (spec §7.9).
	packageLedger, err := view.TestVariantLedger(subjects[0])
	if err != nil {
		return err
	}
	encodedLedger, err := run.EncodeLedger(run.LedgerFromGofresh(packageLedger))
	if err != nil {
		return err
	}

	// Build the test binary before any throttle bracket opens: compilation is
	// a thermal-event source of its own, and the recorded throttled verdict
	// covers the measurement, not the build (spec §9). One build serves every
	// per-benchmark invocation — the build cache is shared — and the artifact
	// itself is discarded.
	warmup, err := os.CreateTemp("", "pew-testbin-*")
	if err != nil {
		return err
	}
	warmupPath := warmup.Name()
	_ = warmup.Close()
	defer os.Remove(warmupPath)
	reportPhase("building " + p.ImportPath)
	if _, err := rc.executeGo(ctx, p.Module.Dir, "", env, run.BuildArgs(p.ImportPath, warmupPath)); err != nil {
		return err
	}
	// The target platform is a per-package truth: it comes from the same
	// toolchain and environment every per-benchmark invocation runs
	// under.
	goos, goarch, err := run.ReadTargetPlatform(p.Module.Dir, env)
	if err != nil {
		return err
	}
	truth := run.ToolchainTruth{GOOS: goos, GOARCH: goarch, ImportPath: p.ImportPath}

	// Single-subject execution (spec §9): each benchmark measures in its own
	// `go test` process, inside its own repository-state bracket, so
	// subjects never share process state and every piece of run evidence —
	// testlog, observation bracket, throttle bracket, state bracket —
	// attributes to exactly one recording. A failing or refused arm
	// discards only its own recording; the package's other arms record,
	// the failures are reported below, and the command exits non-zero. An
	// arm can both fail and be refused (a crashing bench that also moved
	// the tree): both facts surface, neither masks the other.
	armFailed := map[string]error{}
	armRefused := map[string][]string{}
	written := []string{}
	stoppedAt := -1
	for i, name := range runBenches {
		// Interruption stops before the next arm: every arm persisted so
		// far is kept, and the report below says what was kept and what
		// was cut short (spec REQ-pew-interruption). The check precedes
		// the arm's own preparation (its scratch sweep, state snapshot,
		// observation frame), which an arm that will not run must not
		// pay; a cancellation that lands inside an arm is that arm's own
		// error, and one inside the persist window fails its gate, so
		// this check bites only in the instant between two iterations.
		if ctx.Err() != nil {
			stoppedAt = i
			break
		}
		reportPhase(fmt.Sprintf("measuring %s arm %d/%d", p.ImportPath, i+1, len(runBenches)))
		m, refused, err := measureBench(ctx, errw, rc, gc, p, envs.measured(), opts, pkgRel, name, truth, scratch, conditions)
		if err != nil {
			if ctx.Err() != nil {
				stoppedAt = i
				break
			}
			armFailed[name] = err
		}
		if len(refused) > 0 {
			armRefused[name] = refused
		}
		if err != nil || len(refused) > 0 {
			continue
		}
		// Persist the arm the moment it measured (spec
		// REQ-pew-unit-persistence): the write gate — the view still
		// valid, the source inputs' dirtiness, the PGO input unchanged,
		// HEAD unmoved — is re-derived for this arm's own span, so a
		// later arm's failure or an interruption never costs it.
		fp, ok := fingerprints[name]
		if !ok {
			return fmt.Errorf("benchmark %s was not captured in the producer view", name)
		}
		if rc.beforePersist != nil {
			rc.beforePersist(name)
		}
		if err := persistArm(ctx, w, errw, rc, gc, p, prep, view, commit, initialDirty, encodedLedger, name, fp, m, env); err != nil {
			return fmt.Errorf("%s: %w (%d recorded)", name, err, len(written))
		}
		written = append(written, name)
	}
	if stoppedAt >= 0 {
		// The arms from the stopped one on were not measured: the one in
		// flight is lost, the rest never started.
		return interrupted("interrupted: %d recorded, %d not measured in %s", len(written), len(runBenches)-stoppedAt, p.ImportPath)
	}

	var problems []string
	if len(armFailed) > 0 {
		failed := make([]string, 0, len(armFailed))
		for bench := range armFailed {
			failed = append(failed, bench)
		}
		sort.Strings(failed)
		details := make([]string, 0, len(failed))
		for _, bench := range failed {
			details = append(details, bench+": "+armFailed[bench].Error())
		}
		problems = append(problems, fmt.Sprintf("%d benchmark(s) failed: %s",
			len(failed), strings.Join(details, " | ")))
	}
	if len(armRefused) > 0 {
		refused := make([]string, 0, len(armRefused))
		for bench := range armRefused {
			refused = append(refused, bench)
		}
		sort.Strings(refused)
		details := make([]string, 0, len(refused))
		for _, bench := range refused {
			details = append(details, bench+": "+strings.Join(armRefused[bench], "; "))
		}
		problems = append(problems, fmt.Sprintf("refused %d benchmark(s) without recording: %s",
			len(refused), strings.Join(details, " | ")))
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s (%d recorded)", strings.Join(problems, "; "), len(written))
	}
	return nil
}

// persistArm installs one measured arm's recording behind the write
// gate re-derived for this arm: the view still validates (the source
// closures unchanged across the arm's span), the source inputs'
// dirtiness against the recorded commit, the PGO input the compile
// consumed still the effective one, and HEAD still the recorded
// commit. Each recording describes exactly its own invocation: the run
// conditions carry this arm's throttle-bracket delta and the runtime
// evidence is this arm's own digest and manifest.
func persistArm(ctx context.Context, w, errw io.Writer, rc runConfig, gc *gitStateCache, p pkgMeta, prep *packagePreparation, view *gofresh.View, commit string, initialDirty bool, encodedLedger, name string, fp gofresh.Fingerprint, m armMeasurement, env []string) error {
	st, pkgRel, pgoInput := prep.st, prep.pkgRel, prep.pgoInput
	if err := view.Validate(ctx); err != nil {
		return err
	}
	dirty := initialDirty
	if !dirty {
		var err error
		dirty, err = sourceInputsDirty(p.Module.Dir, commit, view.SourceFiles())
		if err != nil {
			return err
		}
	}
	recs := m.recs
	for _, cfg := range run.ProvenanceConfig(commit, dirty, fp.Guards, m.conditions) {
		recs = withConfig(recs, cfg)
	}
	for _, cfg := range fingerprintConfigs(fp, encodedLedger, m.digest, m.manifest) {
		recs = withConfig(recs, cfg)
	}
	// A new GOMAXPROCS variant lineage records loudly, not silently:
	// result names embed the suffix (BenchmarkX-24), so a --pin run
	// mints rows nothing unpinned on record can bridge, and the
	// operator must not learn that from a later stat - after the
	// measurement time is spent (spec §10.1's grouping never bridges
	// suffixes). Warning, never refusal: first recordings and deliberate
	// profile changes are legitimate.
	warnNewVariantLineage(errw, st, pkgRel, name, rc.label, recs)
	// The PGO profile is a build input outside the git-tracked source
	// snapshots, so it gets its own pre-write revalidation: the recorded
	// buildconfig must describe the exact bytes the measured compile
	// consumed.
	goflagsAtWrite, err := run.EffectiveGoflags(p.Module.Dir, env)
	if err != nil {
		return err
	}
	pgoAtWrite, err := run.PGOInput(p.Module.Dir, p.Dir, p.Name == "main", goflagsAtWrite)
	if err != nil {
		return err
	}
	if pgoAtWrite != pgoInput {
		return fmt.Errorf("effective PGO input changed during the benchmark run")
	}
	// The write gate verifies exactly what the recording's validity rests
	// on (spec §9): the fingerprint hashes source inputs, so the view
	// re-validation above proves the source closure unchanged across the
	// arm's span, and the recorded commit must still name HEAD. Non-source
	// worktree residue (a failed arm's crash leftovers) is arm-scoped
	// evidence — the arm that wrote it refused on its own moved state
	// bracket — and never discards a completed sibling measurement.
	stateAtWrite, err := gc.snapshot(p.Module.Dir)
	if err != nil {
		return err
	}
	if stateAtWrite.Commit != commit {
		return fmt.Errorf("repository HEAD moved during the benchmark run")
	}
	if err := st.WriteBatch([]store.WriteRequest{{PkgRel: pkgRel, Bench: name, Label: rc.label, Results: recs}}); err != nil {
		return err
	}
	fmt.Fprintf(w, "%-12s %s.%s\n", "recorded", p.ImportPath, name)
	return nil
}

// armMeasurement is one benchmark's single-subject measurement (spec §9): its
// result rows, its own runtime-input evidence (spec §7.8), and the shared
// pre-run conditions carrying this arm's throttle-bracket delta.
type armMeasurement struct {
	recs             []*benchfmt.Result
	digest, manifest string
	conditions       run.Conditions
}

// measureBench executes exactly one top-level benchmark in its own `go test`
// process (spec §9, single-subject execution): the caller's -bench pattern is
// restricted to this benchmark (sub-benchmark selections preserved), and the
// testlog capture, observation frame, throttle bracket, and repository-state
// bracket are all fresh per invocation, so every piece of evidence attributes
// to exactly this subject. A non-nil error is an arm failure — a suspect
// process records nothing — and non-empty refusal reasons are the spec §9
// arm refusal (sample floor, corruption, or a moved state bracket); both may
// hold at once and both are returned — a crash that also moved the tree
// surfaces as a crash and as a moved bracket, neither masking the other. In
// every case only this arm's recording is discarded, its prior recording
// untouched.
func measureBench(ctx context.Context, errw io.Writer, rc runConfig, gc *gitStateCache, p pkgMeta, env []string, opts run.Options, pkgRel, name string, truth run.ToolchainTruth, scratch []string, base run.Conditions) (armMeasurement, []string, error) {
	pattern, err := restrictBenchmarkPattern(opts.Bench, []string{name})
	if err != nil {
		return armMeasurement{}, nil, err
	}
	armOpts := opts
	armOpts.Bench = pattern
	// Re-sweep the declared run-scratch namespaces before this arm's
	// brackets form (spec §7.8): a prior arm's exited process may have left
	// declared-scratch residue, and left in place it would enter this arm's
	// brackets as pre-existing state — making the manifest depend on
	// sibling order, against §9's (source, subject, machine) claim. The
	// declared-forfeit semantics apply exactly as at command entry, and
	// every removal prints. Non-scratch residue needs no sweep: the arm
	// that wrote it refuses on its own moved state bracket below, and later
	// arms observe it as stable pre-existing state, fail-closed.
	if err := sweepScratchLeftovers(errw, p.Dir, scratch); err != nil {
		return armMeasurement{}, nil, err
	}
	// The arm's repository-state bracket (spec §9): state moved across
	// exactly this invocation refuses exactly this arm, and the next arm's
	// own bracket starts from a fresh snapshot — a refused sibling's
	// residue is pre-existing state to it, never a package abort.
	armStart, err := gc.snapshot(p.Module.Dir)
	if err != nil {
		return armMeasurement{}, nil, err
	}
	// The completed-observation conjunction (spec §7.8), per invocation:
	// the pre-spawn bracket is fingerprinted immediately before this
	// benchmark's process spawns — exec/IO work, outside the throttle
	// bracket — and the measurement invocation carries its own testlog
	// capture through the test binary's flag.
	frame := run.CaptureObservationFrame(ctx, p.Module.Dir, pkgRel)
	testlog, err := os.CreateTemp("", "pew-testlog-*")
	if err != nil {
		return armMeasurement{}, nil, err
	}
	testlogPath := testlog.Name()
	_ = testlog.Close()
	defer os.Remove(testlogPath)
	// run.Execute resolves the working directory and pins PWD to it by
	// construction, so the go driver hands the test binary the same
	// resolved package directory the ingest pins — byte-faithful even
	// through a symlinked checkout, with no per-site bridging.
	throttleBase := rc.snapshotThrottle()
	out, execErr := rc.executeGo(ctx, p.Module.Dir, rc.pin.List(), env, append(run.TestArgs(p.ImportPath, armOpts), "-args", "-test.testlogfile="+testlogPath))
	// Throttling is run-scoped evidence (spec §9): the recorded value is the
	// counter delta across exactly this benchmark's measurement, warned
	// here — the only moment the evidence exists — and fatal under
	// --strict, refusing the suspect arm before anything is recorded.
	throttled := throttleBase.Delta(rc.snapshotThrottle())
	armEnd, err := gc.snapshot(p.Module.Dir)
	if err != nil {
		return armMeasurement{}, nil, err
	}
	var moved []string
	if !armStart.Equal(armEnd) {
		// The recording's evidence premise broke inside this arm's own
		// bracket; the refusal rides alongside any process failure below —
		// both facts reach the package report.
		moved = []string{fmt.Sprintf("repository state moved during %s measurement", name)}
	}
	if execErr != nil {
		// The process is suspect, not merely its transcript (spec §9); it
		// records nothing, while sibling arms — separate processes — are
		// untouched.
		return armMeasurement{}, moved, execErr
	}
	if throttled != nil && *throttled {
		fmt.Fprintf(errw, "pew: warning: thermal throttling occurred during %s.%s measurement\n", p.ImportPath, name)
		if rc.strict {
			return armMeasurement{}, moved, fmt.Errorf("thermal throttling during measurement (--strict)")
		}
	}
	if len(moved) > 0 {
		return armMeasurement{}, moved, nil
	}
	armConditions := base
	armConditions.Throttled = throttled
	runtimeState, err := run.IngestObservation(ctx, frame, testlogPath, "package-test-binary:"+p.ImportPath, env, scratch...)
	if err != nil {
		return armMeasurement{}, nil, err
	}
	// The stream is transient input, not a recording (spec §9): interleaved
	// foreign stdout output corrupts individual result lines, so corruption is
	// surfaced per line and enforced per benchmark — never fatal per line.
	results, corrupt, dropped, err := run.Parse(out)
	if err != nil {
		return armMeasurement{}, nil, err
	}
	// The stream's own toolchain keys are verified against out-of-band
	// truth: a value benchfmt would happily record can still be a
	// dependency's spoof (spec §5, REQ-pew-key-set's value-trust arm).
	if err := run.VerifyToolchainConfig(results, truth); err != nil {
		return armMeasurement{}, nil, err
	}
	for _, cl := range corrupt {
		fmt.Fprintf(errw, "pew: warning: corrupt benchmark output line %d: %q (%s)\n", cl.Line, cl.Text, cl.Cause)
	}
	for _, dc := range dropped {
		fmt.Fprintf(errw, "pew: warning: dropping stream configuration key %q (value %q): not a toolchain benchmark key (spec §5)\n", dc.Key, dc.Value)
	}
	audit := run.AuditStream(results, corrupt, opts.Count, []string{name})
	if audit.PackageCause != "" {
		// This process ran only this benchmark, so evidence the shared-stream
		// model could not localize refuses exactly this arm (spec §9): the
		// destroyed or replaced sample can belong to no other recording.
		return armMeasurement{}, []string{audit.PackageCause}, nil
	}
	reasons := audit.Refused[name]
	// Corruption evidence attributed to any other benchmark is as impossible
	// in a single-subject stream as a foreign result row — the same splice
	// evidence, the same refusal (spec §9). The shared-stream audit drops it
	// as another arm's concern; here there is no other arm.
	for _, cl := range corrupt {
		if cl.Bench != "" && cl.Bench != name {
			reasons = append(reasons, fmt.Sprintf("line %d: %q (%s; names %s, which this single-subject invocation did not run)",
				cl.Line, cl.Text, cl.Cause, cl.Bench))
		}
	}
	if len(reasons) > 0 {
		return armMeasurement{}, reasons, nil
	}
	groups := run.Demux(results, nil)
	if err := requireBenchmarkGroups([]string{name}, groups); err != nil {
		return armMeasurement{}, nil, err
	}
	recs := groups[name]
	delete(groups, name)
	if len(groups) > 0 {
		// The invocation selected exactly one subject, so a parseable result
		// row naming any other benchmark is fabricated or spliced output
		// (spec §9's detection boundary, narrowed by single-subject
		// execution).
		foreign := make([]string, 0, len(groups))
		for other := range groups {
			foreign = append(foreign, other)
		}
		sort.Strings(foreign)
		return armMeasurement{}, []string{fmt.Sprintf("stream carries result rows for %s, which this single-subject invocation did not run", strings.Join(foreign, ", "))}, nil
	}
	return armMeasurement{recs: recs, digest: runtimeState.Digest, manifest: runtimeState.Manifest, conditions: armConditions}, nil, nil
}

func requireBenchmarkGroups(names []string, groups map[string][]*benchfmt.Result) error {
	for _, name := range names {
		if len(groups[name]) == 0 {
			return fmt.Errorf("benchmark %s produced no result", name)
		}
	}
	return nil
}

// validateBenchmarkPattern refuses a -bench pattern that does not
// compile — a flag value, refused at command entry on every verb that
// takes one, before any listing (REQ-pew-preparation).
func validateBenchmarkPattern(pattern string) error {
	_, err := matchingBenchmarks(nil, pattern)
	return err
}

func matchingBenchmarks(names []string, pattern string) ([]string, error) {
	alternatives, err := splitBenchmarkPattern(pattern)
	if err != nil {
		return nil, err
	}
	first := make([]*regexp.Regexp, 0, len(alternatives))
	for _, alternative := range alternatives {
		first = append(first, regexp.MustCompile(alternative[0]))
	}
	var matched []string
	for _, name := range names {
		for _, re := range first {
			if re.MatchString(name) {
				matched = append(matched, name)
				break
			}
		}
	}
	return matched, nil
}

func restrictBenchmarkPattern(pattern string, names []string) (string, error) {
	alternatives, err := splitBenchmarkPattern(pattern)
	if err != nil {
		return "", err
	}
	var restricted []string
	for _, alternative := range alternatives {
		re := regexp.MustCompile(alternative[0])
		var matched []string
		for _, name := range names {
			if re.MatchString(name) {
				matched = append(matched, regexp.QuoteMeta(name))
			}
		}
		if len(matched) == 0 {
			continue
		}
		alternative[0] = "^(?:" + strings.Join(matched, "|") + ")$"
		restricted = append(restricted, strings.Join(alternative, "/"))
	}
	return strings.Join(restricted, "|"), nil
}

// splitBenchmarkPattern mirrors testing's slash- and alternation-aware matcher.
func splitBenchmarkPattern(pattern string) ([][]string, error) {
	var alternatives [][]string
	var parts []string
	start, brackets, parens := 0, 0, 0
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '[':
			brackets++
		case ']':
			if brackets > 0 {
				brackets--
			}
		case '(':
			if brackets == 0 {
				parens++
			}
		case ')':
			if brackets == 0 {
				parens--
			}
		case '\\':
			i++
		case '/', '|':
			if brackets != 0 || parens != 0 {
				continue
			}
			parts = append(parts, pattern[start:i])
			start = i + 1
			if pattern[i] == '|' {
				alternatives = append(alternatives, parts)
				parts = nil
			}
		}
	}
	parts = append(parts, pattern[start:])
	alternatives = append(alternatives, parts)
	for _, alternative := range alternatives {
		for i, part := range alternative {
			part = rewriteBenchmarkPattern(part)
			alternative[i] = part
			if _, err := regexp.Compile(part); err != nil {
				return nil, fmt.Errorf("invalid benchmark pattern %q: %w", pattern, err)
			}
		}
	}
	return alternatives, nil
}

func rewriteBenchmarkPattern(pattern string) string {
	var rewritten []byte
	for _, r := range pattern {
		switch {
		case benchmarkPatternSpace(r):
			rewritten = append(rewritten, '_')
		case !strconv.IsPrint(r):
			quoted := strconv.QuoteRune(r)
			rewritten = append(rewritten, quoted[1:len(quoted)-1]...)
		default:
			rewritten = append(rewritten, string(r)...)
		}
	}
	return string(rewritten)
}

func benchmarkPatternSpace(r rune) bool {
	if r < 0x2000 {
		switch r {
		case '\t', '\n', '\v', '\f', '\r', ' ', 0x85, 0xa0, 0x1680:
			return true
		}
		return false
	}
	if r <= 0x200a {
		return true
	}
	switch r {
	case 0x2028, 0x2029, 0x202f, 0x205f, 0x3000:
		return true
	}
	return false
}

// fingerprintConfigs is the writer-side enumeration of the recording
// lines fingerprintFromConfig reads back into a gofresh.Fingerprint
// (beyond ProvenanceConfig's guard lines). The two enumerations are a
// matched pair pinned end-to-end by TestFingerprintConfigRoundTrip: a
// line dropped on either side breaks the round trip instead of
// silently narrowing the verdict evidence.
func fingerprintConfigs(fp gofresh.Fingerprint, encodedLedger, runtimeDigest, runtimeManifest string) []benchfmt.Config {
	cfgs := []benchfmt.Config{
		run.ClosureConfig(fp.MaximalClosure),
		run.ClosureStrategyConfig(fp.ClosureStrategy),
		run.DynamicStateStrategyConfig(fp.DynamicStateStrategy),
		run.TestVariantConfig(fp.TestVariantClosure),
		run.TestVariantLedgerConfig(encodedLedger),
	}
	cfgs = append(cfgs, run.RuntimeConfig(runtimeDigest, runtimeManifest)...)
	cfgs = append(cfgs, run.GofreshEvidenceConfigs(fp.PurityAssertion, fp.DynamicStateVouches, fp.SingleSubjectDischarges, fp.PackageProcessDischarges)...)
	return cfgs
}

func withConfig(recs []*benchfmt.Result, c benchfmt.Config) []*benchfmt.Result {
	for _, r := range recs {
		r.Config = append(r.Config, c)
	}
	return recs
}

func nonValid(ctx context.Context, errw io.Writer, st *store.Store, e *gofresh.Engine, pkgPath, pkgRel, moduleDir, label string, benches []string, view *gofresh.View) ([]string, error) {
	var need []string
	rows, err := checkPackage(ctx, st, e, pkgPath, pkgRel, moduleDir, benches, label, view)
	if err != nil {
		return nil, err
	}
	for _, b := range benches {
		bv := rows[b]
		v, fp, grownLedger := bv.v, bv.fp, bv.grownLedger
		warnForeignKeys(errw, pkgPath, b, bv.foreign)
		if v == verdictValid && grownLedger != "" {
			// The verdict rode the inert-growth rule (spec §7.9): rewrite
			// the recording under the refreshed compartment pin and current
			// ledger, so later verdicts read plainly valid instead of
			// re-proving the same delta. The run path is the one writer;
			// read-only surfaces never touch the store. The write lands
			// under the recording store, which the repository-state
			// bracket excludes wholesale — pew's outputs are never part of
			// the measured subject (spec §5).
			if err := refreshRecording(st, pkgRel, b, label, fp.TestVariantClosure, grownLedger); err != nil {
				return nil, err
			}
		}
		if v != verdictValid {
			need = append(need, b)
		}
	}
	return need, nil
}

// refreshRecording rewrites a recording's compartment pin and ledger in
// place, leaving every measured row and every other config line untouched.
func refreshRecording(st *store.Store, pkgRel, bench, label, pin, ledger string) error {
	recs, err := st.Read(pkgRel, bench, label)
	if err != nil {
		return err
	}
	for _, r := range recs {
		for i := range r.Config {
			switch r.Config[i].Key {
			case "pew-test-variants":
				r.Config[i].Value = []byte(pin)
			case "pew-test-variant-ledger":
				r.Config[i].Value = []byte(ledger)
			}
		}
	}
	return st.Write(pkgRel, bench, label, recs)
}

// warnNewVariantLineage compares the GOMAXPROCS suffixes of the rows
// about to be written against the stored recording's rows: a suffix
// with no prior lineage while a sibling suffix is on record will not
// bridge in comparisons, and the divergence must surface at record
// time, not at a later stat (spec §10.1).
func warnNewVariantLineage(errw io.Writer, st *store.Store, pkgRel, bench, label string, recs []*benchfmt.Result) {
	prior, err := st.Read(pkgRel, bench, label)
	if err != nil {
		// No readable prior recording (first recording, or an unreadable
		// store): nothing on record to diverge from.
		return
	}
	stored, incoming := lineageSuffixes(prior), lineageSuffixes(recs)
	var others []string
	for old := range stored {
		others = append(others, lineageWord(old))
	}
	sort.Strings(others)
	var warned []string
	for s := range incoming {
		if !stored[s] {
			warned = append(warned, lineageWord(s))
		}
	}
	sort.Strings(warned)
	for _, s := range warned {
		fmt.Fprintf(errw, "pew: warning: %s records a new variant lineage (%s); stored lineage: %s - comparisons will not bridge GOMAXPROCS variants\n", bench, s, strings.Join(others, ", "))
	}
}

// lineageSuffixes maps each result row to its GOMAXPROCS lineage: the
// trailing -<digits> of the name's last path element, or the empty
// lineage for GOMAXPROCS=1 rows, which the testing package emits with
// no suffix at all - a lineage exactly as bridgeless as any other. A
// trailing dash segment that is not all digits (a sub-benchmark case
// name) is part of the name, never a lineage.
func lineageSuffixes(rows []*benchfmt.Result) map[string]bool {
	out := map[string]bool{}
	for _, r := range rows {
		name := string(r.Name)
		if i := strings.LastIndex(name, "/"); i >= 0 {
			name = name[i+1:]
		}
		suffix := ""
		if i := strings.LastIndex(name, "-"); i > 0 && allDigits(name[i+1:]) {
			suffix = name[i:]
		}
		out[suffix] = true
	}
	return out
}

func allDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func lineageWord(suffix string) string {
	if suffix == "" {
		return "unsuffixed"
	}
	return suffix
}
