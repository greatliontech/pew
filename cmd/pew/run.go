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
	benchDir, pin, label string
	opts                 run.Options
	strict, staleOnly    bool
	pure, impure         map[string]bool // benchmark names flagged --assume-pure / --impure
	// throttle snapshots the thermal-throttle counters bracketing each
	// benchmark's measurement invocation (spec §9); nil means
	// run.SnapshotThrottle. A seam so tests control the observed delta
	// deterministically.
	throttle func() run.ThrottleSnapshot
	// execute runs one go-test invocation; nil means run.Execute. A seam so
	// tests can observe invocation order against the throttle bracket.
	execute func(moduleDir, pin string, env, args []string) ([]byte, error)
}

func (rc runConfig) snapshotThrottle() run.ThrottleSnapshot {
	if rc.throttle != nil {
		return rc.throttle()
	}
	return run.SnapshotThrottle()
}

func (rc runConfig) executeGo(moduleDir, pin string, env, args []string) ([]byte, error) {
	if rc.execute != nil {
		return rc.execute(moduleDir, pin, env, args)
	}
	return run.Execute(moduleDir, pin, env, args)
}

func newRunCmd() *cobra.Command {
	var rc runConfig
	rc.opts = run.Options{Count: 10, Benchtime: "1s", Bench: "."}
	var assumePure, impure []string
	cmd := &cobra.Command{
		Use:   "run [packages]",
		Short: guidanceShort("run"),
		Long:  guidanceHelp("run"),
		RunE: func(cmd *cobra.Command, args []string) error {
			rc.pure, rc.impure = toSet(assumePure), toSet(impure)
			for b := range rc.pure {
				if rc.impure[b] {
					return fmt.Errorf("run: %s is both --assume-pure and --impure", b)
				}
			}
			if err := resolveVouches(); err != nil {
				return err
			}
			patterns := args
			if len(patterns) == 0 {
				patterns = []string{"./..."}
			}
			return runRun(cmd.OutOrStdout(), cmd.ErrOrStderr(), rc, patterns)
		},
	}
	f := cmd.Flags()
	f.StringVar(&rc.benchDir, "bench-dir", "", "stored-recordings directory (default <module>/benchmarks)")
	f.IntVar(&rc.opts.Count, "count", 10, "-count: measurement runs per benchmark")
	f.StringVar(&rc.opts.Benchtime, "benchtime", "1s", "-benchtime: duration/iterations per measurement")
	f.StringVar(&rc.opts.Bench, "bench", ".", "-bench: benchmark name pattern")
	f.StringVar(&rc.pin, "pin", "", `pin to CPUs via "taskset -c" (e.g. "2-5"); empty = no pinning`)
	f.BoolVar(&rc.strict, "strict", false, "treat quiesce warnings as fatal")
	f.StringVar(&rc.label, "label", "", "variant label for the recording filename")
	f.StringArrayVar(&assumePure, "assume-pure", nil, "mark a benchmark perf-pure, suppressing Class-B detection (repeatable)")
	f.StringArrayVar(&impure, "impure", nil, "mark a benchmark external / always-rerun (repeatable)")
	f.BoolVar(&rc.staleOnly, "stale", false, "run only benchmarks that are currently non-valid")
	f.StringArrayVar(&rawVouches, "vouch", nil, "dynamic-state vouch IMPORT-PATH:VARIABLE (repeatable): a version-pinned dependency variable accepted as stable after initialization; discharges exactly that variable's shared-dynamic-state downgrade, the load-bearing set recorded as pew-vouches (spec §12)")
	return cmd
}

func toSet(xs []string) map[string]bool {
	if len(xs) == 0 {
		return nil
	}
	m := make(map[string]bool, len(xs))
	for _, x := range xs {
		m[x] = true
	}
	return m
}

func runRun(w, errw io.Writer, rc runConfig, patterns []string) error {
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
	env := os.Environ()
	// The scratch sweep runs at COMMAND ENTRY, before the module state
	// cache pins its baseline: a leftover carrying git-visible files
	// would otherwise enter the cached baseline, and its removal would
	// abort the run as "repository state moved" — the exact failure the
	// sweep exists to prevent.
	for _, p := range pkgs {
		if p.Module.Dir == "" {
			continue
		}
		scratch, err := scratchPatterns(p)
		if err != nil {
			return err
		}
		if err := sweepScratchLeftovers(errw, p.Dir, scratch); err != nil {
			return err
		}
	}
	for _, p := range pkgs {
		if p.Module.Dir == "" {
			continue
		}
		_, _ = gc.state(p.Module.Dir)
	}
	var failures []string
	for _, p := range pkgs {
		if p.Module.Dir == "" {
			continue
		}
		// Like status, a per-package failure (e.g. one that does not build, or
		// an unreadable PGO profile) is reported and does not abort the rest
		// of the tree.
		e, pgoInput, err := newEngineForPkg(p, env)
		if err != nil {
			var pe *toolchainProvenanceError
			if errors.As(err, &pe) {
				return err
			}
			fmt.Fprintf(w, "%-12s %s  (%v)\n", "error", p.ImportPath, err)
			failures = append(failures, p.ImportPath)
			continue
		}
		runErr := runPackage(w, errw, e, gc, rc, p, env, conditions, pgoInput)
		if runErr != nil {
			fmt.Fprintf(w, "%-12s %s  (%v)\n", "error", p.ImportPath, runErr)
			failures = append(failures, p.ImportPath)
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

func runPackage(w, errw io.Writer, e *gofresh.Engine, gc *gitStateCache, rc runConfig, p pkgMeta, env []string, conditions run.Conditions, pgoInput string) error {
	benches, err := selectedBenchmarks(p)
	if err != nil {
		return err
	}
	if len(benches) == 0 {
		return nil
	}
	scratch, err := scratchPatterns(p)
	if err != nil {
		return err
	}
	runBenches, err := matchingBenchmarks(benches, rc.opts.Bench)
	if err != nil {
		return err
	}
	if len(runBenches) == 0 {
		return nil
	}
	dir, err := moduleBenchDir(rc.benchDir, p.Module.Dir)
	if err != nil {
		return err
	}
	st := store.New(dir)
	pkgRel := strings.TrimPrefix(strings.TrimPrefix(p.ImportPath, p.Module.Path), "/")
	baseline, err := gc.state(p.Module.Dir)
	if err != nil {
		return err
	}
	commit, initialDirty := baseline.Commit, baseline.Dirty

	opts := rc.opts
	if rc.staleOnly {
		need, err := nonValid(errw, st, e, p.ImportPath, pkgRel, p.Module.Dir, rc.label, runBenches)
		if err != nil {
			return err
		}
		need = requiredBenchmarks(runBenches, need, rc.impure)
		if len(need) == 0 {
			fmt.Fprintf(w, "%s: all benchmarks valid, nothing to run\n", p.ImportPath)
			return nil
		}
		opts.Bench, err = restrictBenchmarkPattern(opts.Bench, need)
		if err != nil {
			return err
		}
		runBenches = need
	}
	startState, err := gc.snapshot(p.Module.Dir)
	if err != nil {
		return err
	}
	if !baseline.Equal(startState) {
		return fmt.Errorf("repository state moved before benchmark run")
	}

	subjects := make([]gofresh.Subject, 0, len(runBenches))
	for _, name := range runBenches {
		subjects = append(subjects, gofresh.Subject{Package: p.ImportPath, Symbol: name})
	}
	ctx := context.Background()
	view, err := e.NewViewFor(ctx, subjects, p.Module.Dir, gofresh.Measurement)
	if err != nil {
		return err
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
	if _, err := rc.executeGo(p.Module.Dir, "", env, run.BuildArgs(p.ImportPath, warmupPath)); err != nil {
		return err
	}
	// Environment truths are per package, not per process: the classification
	// roots and the target platform come from the same toolchain and
	// environment every per-benchmark invocation runs under.
	envRoots, err := run.ReadGoEnvRoots(p.Module.Dir, env)
	if err != nil {
		return err
	}
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
	measured := make(map[string]armMeasurement, len(runBenches))
	armFailed := map[string]error{}
	armRefused := map[string][]string{}
	for _, name := range runBenches {
		m, refused, err := measureBench(ctx, errw, rc, gc, p, env, opts, pkgRel, name, envRoots, truth, scratch, conditions)
		if err != nil {
			armFailed[name] = err
		}
		if len(refused) > 0 {
			armRefused[name] = refused
		}
		if err == nil && len(refused) == 0 {
			measured[name] = m
		}
	}

	written := []string{}
	if len(measured) > 0 {
		if err := view.Validate(ctx); err != nil {
			return err
		}
		dirty := initialDirty
		if !dirty {
			dirty, err = sourceInputsDirty(p.Module.Dir, commit, view.SourceFiles())
			if err != nil {
				return err
			}
		}
		var writes []store.WriteRequest
		for _, name := range runBenches {
			m, ok := measured[name]
			if !ok {
				continue
			}
			fp, ok := fingerprints[name]
			if !ok {
				return fmt.Errorf("benchmark %s was not captured in the producer view", name)
			}
			recs := m.recs
			// The run conditions carry this arm's own throttle-bracket
			// delta (spec §9), and the runtime-input evidence is this arm's
			// own digest and manifest (spec §7.8) — each recording
			// describes exactly its own invocation.
			for _, cfg := range run.ProvenanceConfig(commit, dirty, fp.Guards, m.conditions) {
				recs = withConfig(recs, cfg)
			}
			for _, cfg := range fingerprintConfigs(fp, encodedLedger, m.digest, m.manifest) {
				recs = withConfig(recs, cfg)
			}
			// Purity flags are per-benchmark (spec §7.5): apply only to the named ones.
			if rc.pure[name] {
				recs = withConfig(recs, run.PureConfig("true"))
			} else if rc.impure[name] {
				recs = withConfig(recs, run.PureConfig("false"))
			}
			// A new GOMAXPROCS variant lineage records loudly, not silently:
			// result names embed the suffix (BenchmarkX-24), so a --pin run
			// on a wider host mints rows nothing on record can bridge, and
			// the operator must not learn that from a later stat - after the
			// measurement time is spent (spec §10.1's grouping never bridges
			// suffixes). Warning, never refusal: first recordings and
			// deliberate profile changes are legitimate.
			warnNewVariantLineage(errw, st, pkgRel, name, rc.label, recs)
			writes = append(writes, store.WriteRequest{PkgRel: pkgRel, Bench: name, Label: rc.label, Results: recs})
			written = append(written, name)
		}
		sort.Slice(writes, func(i, j int) bool { return writes[i].Bench < writes[j].Bench })
		recordingPaths := make([]string, 0, len(writes))
		for _, write := range writes {
			path, err := st.Path(write.PkgRel, write.Bench, write.Label)
			if err != nil {
				return err
			}
			recordingPaths = append(recordingPaths, path)
		}
		if err := rejectStoreCoveredSources(view.SourceFiles(), gc.exclude...); err != nil {
			return err
		}
		if err := rejectRecordingDestinations(view.SourceFiles(), recordingPaths); err != nil {
			return err
		}
		if err := view.Validate(ctx); err != nil {
			return err
		}
		// The PGO profile is a build input outside the git-tracked source
		// snapshots, so it gets its own pre-write revalidation: the recorded
		// buildconfig must describe the exact bytes the measured compile consumed.
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
		// The write gate verifies exactly what the recordings' validity
		// rests on (spec §9): the fingerprints hash source inputs, so the
		// view re-validation above proves the source closures unchanged
		// across the whole measurement span, and the recorded commit must
		// still name HEAD. Non-source worktree residue (a failed arm's
		// crash leftovers) is arm-scoped evidence — the arm that wrote it
		// refused on its own moved state bracket — and never discards
		// completed sibling measurements.
		stateAtWrite, err := gc.snapshot(p.Module.Dir)
		if err != nil {
			return err
		}
		if stateAtWrite.Commit != commit {
			return fmt.Errorf("repository HEAD moved during the benchmark run")
		}
		if err := st.WriteBatch(writes); err != nil {
			return err
		}
		sort.Strings(written)
		for _, name := range written {
			fmt.Fprintf(w, "recorded     %s.%s\n", p.ImportPath, name)
		}
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
func measureBench(ctx context.Context, errw io.Writer, rc runConfig, gc *gitStateCache, p pkgMeta, env []string, opts run.Options, pkgRel, name string, envRoots run.GoEnvRoots, truth run.ToolchainTruth, scratch []string, base run.Conditions) (armMeasurement, []string, error) {
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
	out, execErr := rc.executeGo(p.Module.Dir, rc.pin, env, append(run.TestArgs(p.ImportPath, armOpts), "-args", "-test.testlogfile="+testlogPath))
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
	runtimeState, err := run.IngestObservation(ctx, frame, testlogPath, "package-test-binary:"+p.ImportPath, env, envRoots, scratch...)
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

func requiredBenchmarks(all, stale []string, impure map[string]bool) []string {
	selected := make(map[string]bool, len(stale)+len(impure))
	for _, name := range stale {
		selected[name] = true
	}
	for name := range impure {
		selected[name] = true
	}
	result := make([]string, 0, len(selected))
	for _, name := range all {
		if selected[name] {
			result = append(result, name)
		}
	}
	return result
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

func nonValid(errw io.Writer, st *store.Store, e *gofresh.Engine, pkgPath, pkgRel, moduleDir, label string, benches []string) ([]string, error) {
	var need []string
	rows, err := checkPackage(st, e, pkgPath, pkgRel, moduleDir, benches, label)
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
