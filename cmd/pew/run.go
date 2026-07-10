package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
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
	if warns := run.Quiesce(); len(warns) > 0 {
		for _, x := range warns {
			fmt.Fprintln(errw, "pew: warning:", x)
		}
		if rc.strict {
			return fmt.Errorf("run: refusing to run under noisy conditions (--strict)")
		}
	}
	e, err := newEngine(pkgs)
	if err != nil {
		return err
	}
	gc := newGitStateCache()
	var failures []string
	for _, p := range pkgs {
		if p.Module.Dir == "" {
			continue
		}
		// Like status, a per-package failure (e.g. one that does not build) is
		// reported and does not abort the rest of the tree.
		if err := runPackage(w, e, gc, rc, p); err != nil {
			fmt.Fprintf(w, "%-12s %s  (%v)\n", "error", p.ImportPath, err)
			failures = append(failures, p.ImportPath)
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("run: %d package(s) failed: %s", len(failures), strings.Join(failures, ", "))
	}
	return nil
}

// gitStateCache memoizes gitblob.State per module dir for one invocation — the
// worktree status is the documented slow path on large repos (§11), and a
// multi-package command walks packages of the same module sequentially.
type gitStateCache struct {
	entries map[string]gitStateResult
}

type gitStateResult struct {
	commit string
	dirty  bool
	err    error
}

func newGitStateCache() *gitStateCache {
	return &gitStateCache{entries: map[string]gitStateResult{}}
}

func (c *gitStateCache) state(moduleDir string) (string, bool, error) {
	if r, ok := c.entries[moduleDir]; ok {
		return r.commit, r.dirty, r.err
	}
	commit, dirty, err := gitblob.State(moduleDir)
	c.entries[moduleDir] = gitStateResult{commit: commit, dirty: dirty, err: err}
	return commit, dirty, err
}

func runPackage(w io.Writer, e *gofresh.Engine, gc *gitStateCache, rc runConfig, p pkgMeta) error {
	benches, err := selectedBenchmarks(p)
	if err != nil {
		return err
	}
	if len(benches) == 0 {
		return nil
	}
	commit, dirty, err := gc.state(p.Module.Dir)
	if err != nil {
		return err
	}
	dir := rc.benchDir
	if dir == "" {
		dir = filepath.Join(p.Module.Dir, "benchmarks")
	}
	st := store.New(dir)
	pkgRel := strings.TrimPrefix(strings.TrimPrefix(p.ImportPath, p.Module.Path), "/")

	opts := rc.opts
	if rc.staleOnly {
		need, err := nonValid(st, e, p.ImportPath, pkgRel, p.Module.Dir, rc.label, benches)
		if err != nil {
			return err
		}
		if len(need) == 0 {
			fmt.Fprintf(w, "%s: all benchmarks valid, nothing to run\n", p.ImportPath)
			return nil
		}
		opts.Bench = "^(" + strings.Join(need, "|") + ")$"
	}

	// Capture before the run executes: the recording's guard lines must describe
	// the environment that produced the numbers, and the engine memoizes guards
	// per module, so the per-benchmark Captures below reuse these pre-run values
	// even if the toolchain or build env moves mid-run.
	if _, err := e.Capture(gofresh.Subject{Package: p.ImportPath, Symbol: benches[0]}, p.Module.Dir); err != nil {
		return err
	}

	// A benchmark failure makes `go test` exit non-zero and discards the whole
	// package's run (the successful benches too) — a suspect package records
	// nothing rather than a partial set.
	testlog, err := os.CreateTemp("", "pew-testlog-*.txt")
	if err != nil {
		return err
	}
	testlogPath := testlog.Name()
	if err := testlog.Close(); err != nil {
		return err
	}
	defer os.Remove(testlogPath)

	out, err := run.Execute(p.Module.Dir, rc.pin, run.TestArgs(p.ImportPath, opts, testlogPath))
	if err != nil {
		return err
	}
	testlogBytes, err := os.ReadFile(testlogPath)
	if err != nil {
		return err
	}
	runtimeState, err := runtimeinput.FromTestLog(testlogBytes, p.Module.Dir, filepath.Join(p.Module.Dir, filepath.FromSlash(pkgRel)))
	if err != nil {
		return err
	}
	if !dirty {
		uncommitted, err := runtimeInputsUncommitted(p.Module.Dir, commit, runtimeState.Manifest)
		if err != nil {
			return err
		}
		dirty = uncommitted
	}
	results, err := run.Parse(out)
	if err != nil {
		return err
	}

	written := []string{}
	// The guard values are uniform per module (memoized inside the engine); the
	// closure hash is per benchmark (Tier-2, §7.1). Capture returns both, so the
	// provenance lines are appended per benchmark from its fingerprint.
	for name, recs := range run.Demux(results, nil) {
		fp, err := e.Capture(gofresh.Subject{Package: p.ImportPath, Symbol: name}, p.Module.Dir)
		if err != nil {
			return err
		}
		for _, cfg := range run.ProvenanceConfig(commit, dirty, fp.Guards) {
			recs = withConfig(recs, cfg)
		}
		recs = withConfig(recs, run.ClosureConfig(fp.Closure))
		for _, cfg := range run.RuntimeConfig(runtimeState.Digest, runtimeState.Manifest) {
			recs = withConfig(recs, cfg)
		}
		// Purity flags are per-benchmark (spec §7.5): apply only to the named ones.
		if rc.pure[name] {
			recs = withConfig(recs, run.PureConfig("true"))
		} else if rc.impure[name] {
			recs = withConfig(recs, run.PureConfig("false"))
		}
		if err := st.Write(pkgRel, name, rc.label, recs); err != nil {
			return err
		}
		written = append(written, name)
	}
	sort.Strings(written)
	for _, name := range written {
		fmt.Fprintf(w, "recorded     %s.%s\n", p.ImportPath, name)
	}
	return nil
}

func withConfig(recs []*benchfmt.Result, c benchfmt.Config) []*benchfmt.Result {
	for _, r := range recs {
		r.Config = append(r.Config, c)
	}
	return recs
}

// moduleInspector adapts gitblob's absolute-path ExistsAt to gofresh's
// module-relative CommitInspector.
type moduleInspector struct {
	repo      *gitblob.Repo
	moduleDir string
}

func (m moduleInspector) ExistsAt(commit, moduleRelPath string) (bool, error) {
	return m.repo.ExistsAt(commit, filepath.Join(m.moduleDir, filepath.FromSlash(moduleRelPath)))
}

// runtimeInputsUncommitted reports whether any module-local runtime input in the
// manifest is absent at commit — ignored via .gitignore, untracked, or created
// during the run — so a recording backed by it is not reproducible from that commit
// and must be marked dirty (§5, §7.8, §10). Untracked and modified-tracked inputs
// already flip the git-status dirty flag; this additionally catches .gitignore'd
// inputs, which git status excludes from the worktree status. External (absolute)
// inputs are outside the module's git scope and not considered here.
func runtimeInputsUncommitted(moduleDir, commit, manifest string) (bool, error) {
	repo, err := gitblob.Open(moduleDir)
	if err != nil {
		return false, err
	}
	return runtimeinput.Uncommitted(manifest, commit, moduleInspector{repo: repo, moduleDir: moduleDir})
}

func nonValid(st *store.Store, e *gofresh.Engine, pkgPath, pkgRel, moduleDir, label string, benches []string) ([]string, error) {
	var need []string
	for _, b := range benches {
		v, _, err := checkOne(st, e, pkgPath, pkgRel, moduleDir, b, label)
		if err != nil {
			return nil, err
		}
		if v != verdictValid {
			need = append(need, b)
		}
	}
	return need, nil
}
