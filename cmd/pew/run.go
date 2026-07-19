package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/runtimeinput"
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
	// package's measurement (spec §9); nil means run.SnapshotThrottle. A seam
	// so tests control the observed delta deterministically.
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
		Short: "Run benchmarks with hygiene and store results",
		RunE: func(cmd *cobra.Command, args []string) error {
			rc.pure, rc.impure = toSet(assumePure), toSet(impure)
			for b := range rc.pure {
				if rc.impure[b] {
					return fmt.Errorf("run: %s is both --assume-pure and --impure", b)
				}
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
	gc := newGitStateCache()
	env := os.Environ()
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
	written map[string]map[string]bool
}

type gitStateResult struct {
	state gitblob.RepositoryState
	err   error
}

func newGitStateCache() *gitStateCache {
	return &gitStateCache{
		entries: map[string]gitStateResult{},
		written: map[string]map[string]bool{},
	}
}

func (c *gitStateCache) state(moduleDir string) (gitblob.RepositoryState, error) {
	if r, ok := c.entries[moduleDir]; ok {
		return r.state, r.err
	}
	state, err := gitblob.Snapshot(moduleDir)
	c.entries[moduleDir] = gitStateResult{state: state, err: err}
	return state, err
}

func (c *gitStateCache) writtenPaths(repoRoot string) []string {
	paths := make([]string, 0, len(c.written[repoRoot]))
	for path := range c.written[repoRoot] {
		paths = append(paths, path)
	}
	return paths
}

func (c *gitStateCache) recordWrites(repoRoot string, paths []string) {
	if c.written[repoRoot] == nil {
		c.written[repoRoot] = map[string]bool{}
	}
	for _, path := range paths {
		c.written[repoRoot][path] = true
	}
}

func runPackage(w, errw io.Writer, e *gofresh.Engine, gc *gitStateCache, rc runConfig, p pkgMeta, env []string, conditions run.Conditions, pgoInput string) error {
	benches, err := selectedBenchmarks(p)
	if err != nil {
		return err
	}
	if len(benches) == 0 {
		return nil
	}
	runBenches, err := matchingBenchmarks(benches, rc.opts.Bench)
	if err != nil {
		return err
	}
	if len(runBenches) == 0 {
		return nil
	}
	dir := rc.benchDir
	if dir == "" {
		dir = filepath.Join(p.Module.Dir, "benchmarks")
	}
	dir, err = filepath.Abs(dir)
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
		need, err := nonValid(st, e, p.ImportPath, pkgRel, p.Module.Dir, rc.label, runBenches)
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
	startState, err := gitblob.Snapshot(p.Module.Dir)
	if err != nil {
		return err
	}
	if !baseline.EqualExceptPaths(startState, gc.writtenPaths(baseline.Root())) {
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
		fp, err := view.Capture(subject)
		if err != nil {
			return err
		}
		fingerprints[subject.Symbol] = fp
	}

	// Build the test binary before the throttle bracket opens: compilation is
	// a thermal-event source of its own, and the recorded throttled verdict
	// covers the measurement, not the build (spec §9). The artifact is
	// discarded — the build cache is what the measurement run reuses.
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
	// A benchmark failure makes `go test` exit non-zero and discards the whole
	// package's run (the successful benches too) — a suspect package records
	// nothing rather than a partial set.
	throttleBase := rc.snapshotThrottle()
	out, err := rc.executeGo(p.Module.Dir, rc.pin, env, run.TestArgs(p.ImportPath, opts))
	if err != nil {
		return err
	}
	// Throttling is run-scoped evidence (spec §9): the recorded value is the
	// counter delta across exactly this package's measurement, warned here —
	// the only moment it is observable — and fatal under --strict, refusing
	// the suspect measurement before anything is recorded.
	conditions.Throttled = throttleBase.Delta(rc.snapshotThrottle())
	if conditions.Throttled != nil && *conditions.Throttled {
		fmt.Fprintf(errw, "pew: warning: thermal throttling occurred during %s measurement\n", p.ImportPath)
		if rc.strict {
			return fmt.Errorf("run: %s: thermal throttling during measurement (--strict)", p.ImportPath)
		}
	}
	runtimeObservation, err := runtimeinput.IncompleteEnv(
		p.Module.Dir,
		"package-test-binary:"+p.ImportPath,
		"testlog lacks operation outcome evidence",
		env,
	)
	if err != nil {
		return err
	}
	runtimeState, err := runtimeinput.CompletedState(runtimeObservation)
	if err != nil {
		return err
	}
	// The stream is transient input, not a recording (spec §9): interleaved
	// foreign stdout output corrupts individual result lines, so corruption is
	// surfaced per line and enforced per benchmark — never fatal per line.
	results, corrupt, dropped, err := run.Parse(out)
	if err != nil {
		return err
	}
	for _, cl := range corrupt {
		fmt.Fprintf(errw, "pew: warning: corrupt benchmark output line %d: %q (%s)\n", cl.Line, cl.Text, cl.Cause)
	}
	for _, dc := range dropped {
		fmt.Fprintf(errw, "pew: warning: dropping stream configuration key %q (value %q): not a toolchain benchmark key (spec §5)\n", dc.Key, dc.Value)
	}
	audit := run.AuditStream(results, corrupt, opts.Count, runBenches)
	if audit.PackageCause != "" {
		return fmt.Errorf("benchmark output corrupted: %s", audit.PackageCause)
	}
	refused := make([]string, 0, len(audit.Refused))
	for bench := range audit.Refused {
		refused = append(refused, bench)
	}
	sort.Strings(refused)

	groups := run.Demux(results, nil)
	recordable := make([]string, 0, len(runBenches))
	for _, bench := range runBenches {
		if _, ok := audit.Refused[bench]; ok {
			delete(groups, bench)
			continue
		}
		recordable = append(recordable, bench)
	}
	if err := requireBenchmarkGroups(recordable, groups); err != nil {
		return err
	}
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
	finalState, err := gitblob.Snapshot(p.Module.Dir)
	if err != nil {
		return err
	}
	if !startState.Equal(finalState) {
		return fmt.Errorf("repository state moved during benchmark run")
	}

	written := []string{}
	var writes []store.WriteRequest
	for name, recs := range groups {
		fp, ok := fingerprints[name]
		if !ok {
			return fmt.Errorf("benchmark %s was not captured in the producer view", name)
		}
		for _, cfg := range run.ProvenanceConfig(commit, dirty, fp.Guards, conditions) {
			recs = withConfig(recs, cfg)
		}
		recs = withConfig(recs, run.ClosureConfig(fp.MaximalClosure))
		for _, cfg := range run.RuntimeConfig(runtimeState.Digest, runtimeState.Manifest) {
			recs = withConfig(recs, cfg)
		}
		if fp.PurityAssertion != "" {
			recs = withConfig(recs, run.GofreshPurityConfig(fp.PurityAssertion))
		}
		// Purity flags are per-benchmark (spec §7.5): apply only to the named ones.
		if rc.pure[name] {
			recs = withConfig(recs, run.PureConfig("true"))
		} else if rc.impure[name] {
			recs = withConfig(recs, run.PureConfig("false"))
		}
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
	stateAtWrite, err := gitblob.Snapshot(p.Module.Dir)
	if err != nil {
		return err
	}
	if !finalState.Equal(stateAtWrite) {
		return fmt.Errorf("repository state moved during benchmark validation")
	}
	if err := st.WriteBatch(writes); err != nil {
		return err
	}
	gc.recordWrites(baseline.Root(), recordingPaths)
	sort.Strings(written)
	for _, name := range written {
		fmt.Fprintf(w, "recorded     %s.%s\n", p.ImportPath, name)
	}
	if len(refused) > 0 {
		details := make([]string, 0, len(refused))
		for _, bench := range refused {
			details = append(details, bench+": "+strings.Join(audit.Refused[bench], "; "))
		}
		return fmt.Errorf("output corruption refused %d benchmark(s) (%d recorded): %s",
			len(refused), len(written), strings.Join(details, " | "))
	}
	return nil
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

func withConfig(recs []*benchfmt.Result, c benchfmt.Config) []*benchfmt.Result {
	for _, r := range recs {
		r.Config = append(r.Config, c)
	}
	return recs
}

func nonValid(st *store.Store, e *gofresh.Engine, pkgPath, pkgRel, moduleDir, label string, benches []string) ([]string, error) {
	var need []string
	for _, b := range benches {
		v, _, _, err := checkOne(st, e, pkgPath, pkgRel, moduleDir, b, label)
		if err != nil {
			return nil, err
		}
		if v != verdictValid {
			need = append(need, b)
		}
	}
	return need, nil
}
