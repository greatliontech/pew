package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"

	gofresh "github.com/greatliontech/gofresh"
	"github.com/spf13/cobra"
	"github.com/thegrumpylion/pew/internal/compare"
	"github.com/thegrumpylion/pew/internal/gitblob"
	"github.com/thegrumpylion/pew/internal/gotool"
	"github.com/thegrumpylion/pew/internal/store"
	"golang.org/x/mod/modfile"
	"golang.org/x/perf/benchfmt"
)

type statConfig struct {
	benchDir         string
	label            string
	opts             compare.Options
	failOnRegression bool
}

func newStatCmd() *cobra.Command {
	var sc statConfig
	sc.opts = compare.DefaultOptions()
	var gate string
	cmd := &cobra.Command{
		Use:   "stat [ref | refA refB]",
		Short: "Compare recorded benchmarks across git refs and flag regressions",
		Long: `Compare recorded benchmarks and flag regressions (spec §10).

The baseline mode follows the number of refs:
  pew stat              auto:   working-tree recording vs the HEAD-committed one
  pew stat <ref>        pinned: working-tree recording vs <ref>'s
  pew stat <refA> <refB> A/B:    <refA>'s recording vs <refB>'s

pew stat does not run benchmarks; it compares already-stored results (run them
with 'pew run' first). It inventories stored recording paths from the selected
refs and working-tree store.`,
		Args: cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			gu, err := parseGateUnits(gate)
			if err != nil {
				return err
			}
			sc.opts.GateUnits = gu
			if err := validateOptions(sc.opts); err != nil {
				return err
			}
			return runStat(cmd.OutOrStdout(), cmd.ErrOrStderr(), sc, args)
		},
	}
	f := cmd.Flags()
	f.StringVar(&sc.benchDir, "bench-dir", "", "stored-recordings directory (default <module>/benchmarks)")
	f.StringVar(&sc.label, "label", "", "variant label to compare (spec §6); empty = the unlabeled recording")
	f.Float64Var(&sc.opts.Alpha, "alpha", sc.opts.Alpha, "significance level for the Mann-Whitney U test")
	f.Float64Var(&sc.opts.ThresholdPct, "threshold", sc.opts.ThresholdPct, "regression magnitude floor, in percent")
	f.Float64Var(&sc.opts.Confidence, "confidence", sc.opts.Confidence, "confidence level for summary intervals")
	f.BoolVar(&sc.failOnRegression, "fail-on-regression", false, "exit non-zero if a gated metric regresses")
	f.StringVar(&gate, "gate", "sec/op", "comma-separated units whose regression fails the build (sec/op, B/op, allocs/op)")
	return cmd
}

// validateOptions rejects out-of-range tunables that would silently corrupt the
// regression criterion (spec §10.1): α and confidence outside (0,1), or a
// negative magnitude floor (which would make |Δ| ≥ threshold always true and gut
// condition (3)). A zero threshold is allowed — it means "any significant worse
// change regresses", a legitimate (noisier) choice.
func validateOptions(o compare.Options) error {
	if o.Alpha <= 0 || o.Alpha >= 1 {
		return fmt.Errorf("stat: --alpha must be in (0,1), got %v", o.Alpha)
	}
	if o.Confidence <= 0 || o.Confidence >= 1 {
		return fmt.Errorf("stat: --confidence must be in (0,1), got %v", o.Confidence)
	}
	if o.ThresholdPct < 0 {
		return fmt.Errorf("stat: --threshold must be ≥ 0, got %v", o.ThresholdPct)
	}
	return nil
}

var knownUnits = map[string]bool{"sec/op": true, "B/op": true, "allocs/op": true}

func parseGateUnits(s string) (map[string]bool, error) {
	out := map[string]bool{}
	for _, tok := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' }) {
		if !knownUnits[tok] {
			return nil, fmt.Errorf("stat: unknown --gate unit %q (want sec/op, B/op, or allocs/op)", tok)
		}
		out[tok] = true
	}
	if len(out) == 0 {
		return nil, errors.New("stat: --gate must list at least one unit")
	}
	return out, nil
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
}

type statModule struct {
	modulePath string
	moduleDir  string
	benchDir   string
	store      *store.Store
	repo       *gitblob.Repo
	keys       map[statKey]bool
	current    map[statKey]currentBench
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

func runStat(w, errw io.Writer, sc statConfig, refs []string) error {
	bl, err := baselineFor(refs)
	if err != nil {
		return err
	}
	pkgs, err := statPackages(bl, errw)
	if err != nil {
		return err
	}
	repo, err := gitblob.Open(".")
	if err != nil {
		return err
	}

	modules, err := statModules(pkgs, sc, errw)
	if err != nil {
		return err
	}
	scanRoots, err := historicalScanRoots(modules)
	if err != nil {
		return err
	}
	modules, err = addHistoricalModules(modules, repo, bl.historicalRefs(), sc, scanRoots)
	if err != nil {
		return err
	}
	modules = dedupeStatModules(modules)
	var baseAll, newAll []*benchfmt.Result

	// When the new side is the working-tree recording (auto/pinned), a recording
	// that is stale for HEAD does not reflect current code, so its "new" numbers
	// are misleading — warn (don't block), pointing at `pew run`. The engine is
	// only needed for that check, so it is built only then — via newEngine, so
	// stat honors //gofresh:pure directives exactly as status and run do (§7.5:
	// every consumer of the shared engine). In A/B mode both sides are historical
	// recordings and no staleness check applies.
	var engine *gofresh.Engine
	if bl.newRef == "" {
		if engine, err = newEngine(pkgs); err != nil {
			return err
		}
	}

	for _, m := range modules {
		if err := addStatInventory(m, bl, sc.label); err != nil {
			return err
		}
		checkStale := bl.newRef == ""

		for _, key := range sortedStatKeys(m.keys) {
			baseRecs, baseOK, err := readSide(m.store, m.repo, bl.baseRef, key.pkgRel, key.bench, key.label)
			if err != nil {
				return err
			}
			newRecs, newOK, err := readSide(m.store, m.repo, bl.newRef, key.pkgRel, key.bench, key.label)
			if err != nil {
				return err
			}
			if !baseOK && !newOK {
				continue // never recorded on either side — nothing to say
			}
			// A dirty recording's commit does not faithfully describe its source
			// (§5), so it is never usable as a baseline (§5, §10: "Pinned refs must
			// resolve to non-dirty recordings"). A baseline always comes from a ref
			// (base side in every mode; the new side too in A/B); the working-tree
			// side (newRef=="") is the code under test and may be dirty. Skip a
			// dirty baseline rather than report a verdict against unfaithful numbers.
			if baseOK && isDirty(baseRecs) {
				fmt.Fprintf(errw, "pew: warning: baseline %s:%s is a dirty recording; skipping (spec §10)\n", bl.baseRef, key.bench)
				continue
			}
			if newOK && bl.newRef != "" && isDirty(newRecs) {
				fmt.Fprintf(errw, "pew: warning: baseline %s:%s is a dirty recording; skipping (spec §10)\n", bl.newRef, key.bench)
				continue
			}
			if checkStale && newOK {
				cur, ok := m.current[key]
				if !ok {
					fmt.Fprintf(errw, "pew: warning: working-tree recording %s.%s has no current benchmark declaration; comparison may not reflect HEAD — re-run `pew run`\n", key.pkgRel, key.bench)
				} else {
					// Best-effort: a check failure warns but never blocks the comparison.
					fp, pure := fingerprintFromConfig(newRecs[0].Config)
					subj := gofresh.Subject{Package: cur.importPath, Symbol: key.bench}
					if v, e := engine.Check(fp, subj, cur.moduleDir, gofresh.Measurement); e != nil {
						fmt.Fprintf(errw, "pew: warning: %s.%s: cannot check working-tree staleness: %v\n", cur.importPath, key.bench, e)
					} else if v = applyPurity(v, pure); v.Status != gofresh.Valid {
						msg := string(v.Status)
						if v.Reason != "" {
							msg += " (" + v.Reason + ")"
						}
						fmt.Fprintf(errw, "pew: warning: working-tree recording %s.%s is %s; comparison may not reflect HEAD — re-run `pew run`\n", cur.importPath, key.bench, msg)
					}
				}
			}
			baseAll = append(baseAll, baseRecs...)
			newAll = append(newAll, newRecs...)
		}
	}

	res := compare.Compare(baseAll, newAll, sc.opts)
	if len(res.Tables) == 0 && len(res.Notes) == 0 {
		fmt.Fprintln(w, "no recorded benchmarks to compare")
		return nil
	}
	if err := res.WriteText(w); err != nil {
		return err
	}
	if sc.failOnRegression && res.Regressed() {
		return errors.New("regression detected")
	}
	return nil
}

func statPackages(bl baseline, errw io.Writer) ([]pkgMeta, error) {
	pkgs, err := resolvePackages([]string{"./..."})
	if err != nil {
		fallback, fallbackErr := fallbackStatPackages()
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
	fallback, err := fallbackStatPackages()
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
			dir, err := statBenchDir(p.Module.Dir, sc.benchDir)
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
			continue
		}
		pkgRel := strings.TrimPrefix(strings.TrimPrefix(p.ImportPath, p.Module.Path), "/")
		for _, b := range benches {
			m.current[statKey{pkgRel: pkgRel, bench: b, label: sc.label}] = currentBench{importPath: p.ImportPath, moduleDir: p.Module.Dir}
		}
	}
	mods := make([]*statModule, 0, len(byDir))
	for _, m := range byDir {
		mods = append(mods, m)
	}
	sort.Slice(mods, func(i, j int) bool { return mods[i].moduleDir < mods[j].moduleDir })
	return mods, nil
}

func statBenchDir(moduleDir, benchDir string) (string, error) {
	if benchDir == "" {
		return filepath.Join(moduleDir, "benchmarks"), nil
	}
	if filepath.IsAbs(benchDir) {
		return benchDir, nil
	}
	return filepath.Abs(benchDir)
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

func addHistoricalModules(mods []*statModule, repo *gitblob.Repo, refs []string, sc statConfig, scanRoots []string) ([]*statModule, error) {
	byDir := map[string]*statModule{}
	for _, m := range mods {
		byDir[m.moduleDir] = m
	}
	for _, ref := range refs {
		for _, root := range scanRoots {
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
				benchDir, err := statBenchDir(moduleDir, sc.benchDir)
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

func fallbackStatPackages() ([]pkgMeta, error) {
	p, err := currentModulePackage()
	if err != nil {
		return nil, err
	}
	return []pkgMeta{p}, nil
}

func currentModulePackage() (pkgMeta, error) {
	out, err := gotool.Run("list", "-m", "-json")
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
		recs, err := m.store.List()
		if err != nil {
			return err
		}
		for _, r := range recs {
			if r.Label == label {
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
		recs, sideOK, err := readSide(m.store, m.repo, ref, r.PkgRel, r.Bench, r.Label)
		if err != nil {
			return err
		}
		if !sideOK || !store.IsRecording(recs) {
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

// isDirty reports whether a recording was made on a dirty working tree (§5). The
// flag is uniform across a recording's results (one overwrite-written block), so
// the first result decides.
func isDirty(recs []*benchfmt.Result) bool {
	return len(recs) > 0 && recs[0].GetConfig("dirty") == "true"
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
		if err != nil {
			return nil, false, err
		}
		if !store.IsRecording(recs) {
			return nil, false, nil
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
	if err != nil {
		return nil, false, err
	}
	if !store.IsRecording(recs) {
		return nil, false, nil
	}
	return recs, true, nil
}
