package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/greatliontech/gofresh/guard"
	"github.com/spf13/cobra"

	"github.com/greatliontech/pew/internal/compare"
	"github.com/greatliontech/pew/internal/gitblob"
	"github.com/greatliontech/pew/internal/gotool"
	"github.com/greatliontech/pew/internal/profiles"
	"github.com/greatliontech/pew/internal/run"
	"golang.org/x/perf/benchfmt"
)

// abConfig carries pew ab's seams, mirroring runConfig's: tests observe
// build/execute ordering without real toolchain work.
type abConfig struct {
	diagnostic                func(context.Context, string, string, gotool.Environment, string, []string, io.Writer) ([]byte, error)
	profile, profileBenchtime string
	jsonOut                   bool
	artifact                  *abArtifact
	bench                     string
	count                     int
	benchtime                 string
	ref                       string
	pin                       run.Pin // the derived CPU set both sides run on; unpinned when empty
	// worktreeDir is the operator's placement for side B's worktree and
	// both sides' binaries — a same-device directory named where the
	// default sibling placement is unavailable (an unwritable parent, a
	// repository at a mount boundary); empty selects the sibling.
	worktreeDir string
	strict      bool
	out         string
	throttle    func() run.ThrottleSnapshot
	execute     func(dir, pin string, env []string, bin string, args []string) ([]byte, error)
	build       func(dir string, env []string, args []string) error
	guards      func(ctx context.Context, moduleDir, pkgDir string, mainPkg bool, env []string) (guard.Guards, error)
}

// sideGuards captures one side's comparison-guard values in that side's own
// tree: the toolchain and build-config guards are the side's build identity
// (a ref pinning a different toolchain directive, or shipping a different
// PGO profile, is a genuine mismatch the comparator must refuse), while the
// machine and runtime-config guards are process facts the two sides share by
// construction. The PGO profile rides in as a content digest exactly as the
// recording path's engine takes it.
func (ac abConfig) sideGuards(ctx context.Context, moduleDir, pkgDir string, mainPkg bool, env gotool.Environment) (guard.Guards, error) {
	if ac.guards != nil {
		return ac.guards(ctx, moduleDir, pkgDir, mainPkg, env.Values())
	}
	// One pass, one reader: the effective GOFLAGS and the guards'
	// capture read the same `go env -json` document.
	reader := gotool.Reader(moduleDir, env, dependencies(ctx).prepare)
	goflags, err := run.EffectiveGoflags(ctx, reader)
	if err != nil {
		return guard.Guards{}, err
	}
	pgo, err := run.PGOInput(moduleDir, pkgDir, mainPkg, goflags)
	if err != nil {
		return guard.Guards{}, err
	}
	var buildInputs []string
	if pgo != "" {
		buildInputs = append(buildInputs, pgo)
	}
	// Build readers use the analysis environment; runtime guards describe the
	// actual measured environment, including the selected pin's GOMAXPROCS.
	return guard.Capture(ctx, reader, newEnvironments(env, ac.pin).measured().Values(), guard.Measurement, buildInputs...)
}

// abPackageName resolves a package directory's package name with the same
// toolchain environment the builds use.
func abPackageName(ctx context.Context, dir string, env gotool.Environment) (string, error) {
	out, err := gotool.List(ctx, dir, env, "-f", "{{.Name}}", ".")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func abPackageMetadata(ctx context.Context, dir string, env gotool.Environment) (pkgMeta, error) {
	data, err := gotool.List(ctx, dir, env, "-json", ".")
	if err != nil {
		return pkgMeta{}, err
	}
	var p pkgMeta
	err = json.Unmarshal(data, &p)
	return p, err
}

func (ac abConfig) snapshotThrottle() run.ThrottleSnapshot {
	if ac.throttle != nil {
		return ac.throttle()
	}
	return run.SnapshotThrottle()
}

func (ac abConfig) executeBinary(ctx context.Context, dir, pin string, env gotool.Environment, bin string, args []string) ([]byte, error) {
	if ac.execute != nil {
		return ac.execute(dir, pin, env.Values(), bin, args)
	}
	return run.ExecuteBinaryContext(ctx, dir, pin, env, bin, args)
}

func (ac abConfig) buildBinary(ctx context.Context, dir string, env gotool.Environment, args []string) error {
	if ac.build != nil {
		return ac.build(dir, env.Values(), args)
	}
	// The same process-group runner the measurements use: a cancelled
	// build takes its compile and link children with it and reports the
	// cancellation, not a build failure.
	return run.BuildContext(ctx, dir, env, args)
}

func newABCmd() *cobra.Command {
	ac := abConfig{}
	var pin bool
	cmd := &cobra.Command{
		Use:   "ab [packages]",
		Short: guidanceShort("ab"),
		Long:  guidanceHelp("ab"),
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// The flag values the verb alone decides refuse here, before
			// any listing (REQ-pew-preparation).
			if ac.count < 1 {
				return fmt.Errorf("ab: count must be at least 1")
			}
			if err := validateBenchmarkPattern(ac.bench); err != nil {
				return err
			}
			if pin {
				derived, err := derivePin(cmd.ErrOrStderr())
				if err != nil {
					return fmt.Errorf("ab: %w", err)
				}
				ac.pin = derived
			}
			ctx, stop := commandContext(cmd)
			defer stop()
			return runAB(ctx, cmd.OutOrStdout(), cmd.ErrOrStderr(), ac, args)
		},
	}
	f := cmd.Flags()
	f.StringVar(&ac.profile, "profile", "", "capture independent diagnostics on both sides")
	f.Lookup("profile").NoOptDefVal = "cpu,alloc"
	f.StringVar(&ac.profileBenchtime, "profile-benchtime", "1s", "diagnostic execution budget")
	f.BoolVar(&ac.jsonOut, "json", false, "emit typed comparison reports")
	f.StringVar(&ac.bench, "bench", ".", "")
	f.IntVar(&ac.count, "count", 6, "")
	f.StringVar(&ac.benchtime, "benchtime", "", "")
	f.StringVar(&ac.ref, "ref", "HEAD", "")
	f.BoolVar(&pin, "pin", false, "")
	f.StringVar(&ac.worktreeDir, "worktree-dir", "", "")
	f.BoolVar(&ac.strict, "strict", false, "")
	f.StringVar(&ac.out, "out", "", "")
	return cmd
}

func runAB(ctx context.Context, w, errw io.Writer, ac abConfig, patterns []string) (result error) {
	defer func() {
		if ctx.Err() != nil {
			var stopped *interruptedError
			if !errors.As(result, &stopped) {
				result = errors.Join(interrupted("ab: interrupted; completed units retained"), result)
			}
		}
	}()
	if _, err := profiles.Kinds(ac.profile); err != nil {
		return err
	}
	if ac.profile != "" {
		if ac.profileBenchtime == "" {
			ac.profileBenchtime = "1s"
		}
		if err := profiles.ValidateBudget(ac.profileBenchtime); err != nil {
			return err
		}
	}
	ac.artifact = &abArtifact{path: ac.out}
	analysis, err := gotool.NewEnvironment(nil)
	if err != nil {
		return err
	}
	if ac.count < 1 {
		return fmt.Errorf("ab: count must be at least 1")
	}
	reportPhase(ctx, "listing")
	pkgs, err := resolvePackages(ctx, analysis, patterns)
	if err != nil {
		if cancelledBy(ctx, err) {
			return interrupted("ab: interrupted while listing packages; no pair measured")
		}
		return err
	}
	if len(pkgs) == 0 {
		return fmt.Errorf("ab: no packages matched")
	}
	output, err := prepareABOutput(ctx, ac.out, ac.profile != "", pkgs, analysis)
	if err != nil {
		return err
	}
	if output != nil {
		ac.out = output.path
		ac.artifact.path = output.path
		ac.artifact.ownership = output
	}
	// The machine-prep gate is the recording protocol's (spec §9): the
	// derivation loop deserves the same floor, and --strict the same
	// teeth.
	conditions := run.ObserveConditions()
	if warns := conditions.Warnings(); len(warns) > 0 {
		for _, x := range warns {
			fmt.Fprintln(errw, "pew: warning:", x)
		}
		if ac.strict {
			return fmt.Errorf("ab: refusing to run under noisy conditions (--strict)")
		}
	}
	moduleDir := pkgs[0].Module.Dir
	if moduleDir == "" {
		return fmt.Errorf("ab: packages outside a module cannot be compared")
	}
	control := abGitControl{env: analysis}
	repoRoot, err := control.topLevel(ctx, moduleDir)
	if err != nil {
		return err
	}
	placement, err := abPlacement(repoRoot, ac.worktreeDir)
	if err != nil {
		return err
	}
	// A killed run's side-B worktree is durable residue beside the
	// repository: swept here, before this run mints its own, by the same
	// cross-check `git worktree prune` trusts.
	sweptPaths, err := control.sweep(ctx, repoRoot, placement)
	if err != nil {
		return err
	}
	for _, swept := range sweptPaths {
		fmt.Fprintf(errw, "pew: swept a stale side-B worktree %s (a killed run's residue)\n", swept)
	}
	// B side: the ref materialized in a disposable worktree - never a
	// stash, never a mutation of the working tree; a crash leaves a
	// removable directory and a writable repository.
	worktree, cleanup, err := control.add(ctx, repoRoot, placement, ac.ref)
	if cleanup != nil {
		defer func() {
			gate, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			result = errors.Join(result, cleanup(gate))
		}()
	}
	if err != nil {
		return err
	}
	envs := newEnvironments(analysis, ac.pin)
	env := envs.analysis
	if output != nil {
		reportPhase(ctx, "checking reference output ownership")
		var reference []pkgMeta
		for _, p := range pkgs {
			_, dir, err := abReferenceDirs(p, repoRoot, worktree, ac.ref)
			if err != nil {
				return err
			}
			b, err := abPackageMetadata(ctx, dir, env)
			if err != nil {
				return err
			}
			reference = append(reference, b)
		}
		if err := output.includePackages(ctx, reference, env); err != nil {
			return err
		}
	}
	// Every package prepares before any package measures (spec
	// REQ-pew-preparation): containment, the B side's existence, both
	// trees' benchmark declarations against the pattern, both builds,
	// both guard captures, and the guard comparison — so a later
	// package's refusal is known before an earlier package spends its
	// 2 × count iterations, and a guard mismatch never reaches a first
	// iteration.
	var preps []*abPreparation
	for i, p := range pkgs {
		preparing := func() error {
			return interrupted("ab: interrupted while preparing %s (package %d/%d); nothing compared", p.ImportPath, i+1, len(pkgs))
		}
		if ctx.Err() != nil {
			return preparing()
		}
		reportPhase(ctx, fmt.Sprintf("preparing %s (%d/%d)", p.ImportPath, i+1, len(pkgs)))
		prep, err := prepareABPackage(ctx, ac, p, repoRoot, worktree, placement, env)
		if prep != nil && prep.tmp != "" {
			defer func() {
				if err := os.RemoveAll(prep.tmp); err != nil {
					result = errors.Join(result, fmt.Errorf("ab: binary cleanup %s: %w", prep.tmp, err))
				}
			}()
		}
		if cancelledBy(ctx, err) {
			return preparing()
		}
		if err != nil {
			return err
		}
		preps = append(preps, prep)
	}
	if output != nil {
		if err := output.validate(); err != nil {
			return err
		}
	}
	for i, prep := range preps {
		if err := ctx.Err(); err != nil {
			return interrupted("ab: interrupted before %s (%d of %d packages compared)", prep.pkg.ImportPath, i, len(preps))
		}
		if err := abPackage(ctx, w, errw, ac, prep, i+1, len(preps), envs); err != nil {
			return err
		}
	}
	if ac.profile != "" {
		if err := captureABProfiles(ctx, w, errw, ac, preps, envs, conditions); err != nil {
			return err
		}
	}
	return interruptedAfterLastUnit(ctx, "ab: interrupted after the last package; every completed pair is in its artifact")
}

// abPreparation is one package's A/B preparation record: both sides
// located, built, and guard-captured, the guards agreeing on every
// comparison key, before the first iteration.
type abPreparation struct {
	diagnostics      [2]*abProfileSide
	rows             [2][]*benchfmt.Result
	pkg              pkgMeta
	sideBPkgDir      string
	tmp, binA, binB  string
	guardsA, guardsB guard.Guards
}

// prepareABPackage builds a package's A/B preparation record, firing
// every refusal the two trees decide before any iteration; a returned
// record with a temp dir owns it even when the error is non-nil.
func prepareABPackage(ctx context.Context, ac abConfig, p pkgMeta, repoRoot, worktree, placement string, env gotool.Environment) (*abPreparation, error) {
	sideBModule, sideBPkgDir, err := abReferenceDirs(p, repoRoot, worktree, ac.ref)
	if err != nil {
		return nil, err
	}
	pkgRelToModule, err := filepath.Rel(p.Module.Dir, p.Dir)
	if err != nil {
		return nil, err
	}
	// The pattern must name a benchmark on both sides: the declarations
	// are parseable from source before any build or run.
	if err := abPatternSelects(p, sideBPkgDir, ac); err != nil {
		return nil, err
	}
	prep := &abPreparation{pkg: p, sideBPkgDir: sideBPkgDir}
	// Both sides build BEFORE either side measures: two standing
	// binaries make interleaving free, and the shared build cache makes
	// the second build cheap. Every package's binaries stand for the
	// whole run (preparation precedes the first iteration), so they
	// live beside the repository like the worktree does — never under a
	// temp root that may be memory-backed, where a tree's worth of test
	// binaries would perturb the measurement they serve.
	tmp, err := os.MkdirTemp(placement, ".pew-ab-bin-*")
	if err != nil {
		return nil, err
	}
	prep.tmp = tmp
	prep.binA = filepath.Join(tmp, "a.test")
	prep.binB = filepath.Join(tmp, "b.test")
	beforeA, err := gitblob.Snapshot(p.Module.Dir)
	if err != nil {
		return prep, err
	}
	beforeB, err := gitblob.Snapshot(sideBModule)
	if err != nil {
		return prep, err
	}
	nameB, err := abPackageName(ctx, sideBPkgDir, env)
	if err != nil {
		return prep, err
	}
	guardsA, err := ac.sideGuards(ctx, p.Module.Dir, p.Dir, p.Name == "main", env)
	if err != nil {
		return prep, err
	}
	guardsB, err := ac.sideGuards(ctx, sideBModule, sideBPkgDir, nameB == "main", env)
	if err != nil {
		return prep, err
	}
	if ac.profile != "" {
		prep.diagnostics, err = prepareABProfiles(ctx, ac, p, sideBPkgDir, newEnvironments(env, ac.pin))
		if err != nil {
			return prep, err
		}
		if ac.artifact != nil && ac.artifact.ownership != nil {
			for _, side := range prep.diagnostics {
				ac.artifact.ownership.sources = append(ac.artifact.ownership.sources, side.view.SourceFiles()...)
			}
			if err := ac.artifact.ownership.validate(); err != nil {
				return prep, err
			}
		}
	}
	// Builds run at each side's MODULE root with a relative package
	// target, exactly as the recording path builds: a relative -pgo in
	// GOFLAGS resolves against the build cwd, and the guard digest pins
	// the module-root resolution — building anywhere else lets the
	// compile consume bytes the guard never digested.
	relTarget := "."
	if pkgRelToModule != "." {
		relTarget = "./" + filepath.ToSlash(pkgRelToModule)
	}
	if err := ac.buildBinary(ctx, p.Module.Dir, env, []string{"test", "-c", "-o", prep.binA, relTarget}); err != nil {
		return prep, fmt.Errorf("ab: building side A (working tree): %w", err)
	}
	if err := ac.buildBinary(ctx, sideBModule, env, []string{"test", "-c", "-o", prep.binB, relTarget}); err != nil {
		return prep, fmt.Errorf("ab: building side B (%s): %w", ac.ref, err)
	}
	// Guards are captured at build time, not after measurement: the stamp
	// claims the BUILD identity of the two standing binaries, and the
	// repository stays writable throughout — a mid-measurement edit to
	// default.pgo or go.mod must not rewrite what the already-built sides
	// are claimed to be. Capture failure also surfaces before the
	// measurement spend, not after it. Side B's package kind comes from
	// the ref's own tree: a package that is main at the ref resolves its
	// default.pgo there regardless of what the working tree renamed.
	prep.guardsA, err = ac.sideGuards(ctx, p.Module.Dir, p.Dir, p.Name == "main", env)
	if err != nil {
		return prep, fmt.Errorf("ab: capturing side A guards: %w", err)
	}
	prep.guardsB, err = ac.sideGuards(ctx, sideBModule, sideBPkgDir, nameB == "main", env)
	if err != nil {
		return prep, fmt.Errorf("ab: capturing side B guards: %w", err)
	}
	// A guard mismatch is refused HERE, before the first iteration (spec
	// §12): the comparator would only note it after the whole
	// measurement, which is the spend the refusal exists to save.
	if err := abGuardsAgree(p.ImportPath, ac.ref, prep.guardsA, prep.guardsB); err != nil {
		return prep, err
	}
	afterA, err := gitblob.Snapshot(p.Module.Dir)
	if err != nil {
		return prep, err
	}
	afterB, err := gitblob.Snapshot(sideBModule)
	if err != nil {
		return prep, err
	}
	if !beforeA.Equal(afterA) || !beforeB.Equal(afterB) || guardsA != prep.guardsA || guardsB != prep.guardsB {
		return prep, fmt.Errorf("ab: source or guards moved across build")
	}
	for _, side := range prep.diagnostics {
		if side != nil {
			if err := side.view.Validate(ctx); err != nil {
				return prep, err
			}
		}
	}
	return prep, nil
}

// abReferenceDirs is the common location mapping for reference-side ownership
// inventory and builds. Modules outside the repository have no materialized side.
func abReferenceDirs(p pkgMeta, repoRoot, worktree, ref string) (string, string, error) {
	moduleRel, err := filepath.Rel(repoRoot, p.Module.Dir)
	if err != nil || strings.HasPrefix(moduleRel, "..") {
		return "", "", fmt.Errorf("ab: package %s lives in a module outside this repository (%s)", p.ImportPath, p.Module.Dir)
	}
	pkgRel, err := filepath.Rel(p.Module.Dir, p.Dir)
	if err != nil {
		return "", "", err
	}
	module := filepath.Join(worktree, moduleRel)
	dir := filepath.Join(module, pkgRel)
	if _, err := os.Stat(dir); err != nil {
		return "", "", fmt.Errorf("ab: package %s does not exist at %s: %w", p.ImportPath, ref, err)
	}
	return module, dir, nil
}

// abPatternSelects refuses a pattern naming no benchmark on either side.
func abPatternSelects(p pkgMeta, sideBPkgDir string, ac abConfig) error {
	benchesA, err := selectedBenchmarks(p)
	if err != nil {
		return err
	}
	setB, _, err := sourceBenchmarks(sideBPkgDir)
	if err != nil {
		return fmt.Errorf("ab: scanning side B benchmarks at %s: %w", ac.ref, err)
	}
	benchesB := make([]string, 0, len(setB))
	for b := range setB {
		benchesB = append(benchesB, b)
	}
	// Side A first: the listing's declarations are exact, the B side's
	// scan is over every test file in the directory.
	for _, side := range []struct {
		name    string
		benches []string
	}{{"side A", benchesA}, {"side B", benchesB}} {
		matched, err := matchingBenchmarks(side.benches, ac.bench)
		if err != nil {
			return err
		}
		if len(matched) == 0 {
			return fmt.Errorf("ab: %s: pattern %q selects no benchmark on %s", p.ImportPath, ac.bench, side.name)
		}
	}
	return nil
}

// abGuardsAgree refuses a B side whose build identity differs from A's
// on any comparison guard — the four rows spec §5's `guard?` column
// marks, judged in table order (run.GuardConfig's order, the order the
// comparison names its guards in too) — naming the first differing
// guard and both values.
// Two live captures compare by value alone: a stored recording's
// completeness rule (an empty recorded guard is a mismatch) is not
// theirs, and no guard's emptiness may shadow a later guard's
// difference. Two sides captured on one host from one environment
// share the machine and runtime-configuration guards by construction;
// the toolchain directive and the PGO bytes are the ones a ref can
// change.
func abGuardsAgree(importPath, ref string, a, b guard.Guards) error {
	sideA, sideB := run.GuardConfig(a), run.GuardConfig(b)
	for i := range sideA {
		if va, vb := string(sideA[i].Value), string(sideB[i].Value); va != vb {
			return fmt.Errorf("ab: %s: %s mismatch (A=%s B=%s at %s); refusing before measurement", importPath, sideA[i].Key, va, vb, ref)
		}
	}
	return nil
}

func abPackage(ctx context.Context, w, errw io.Writer, ac abConfig, prep *abPreparation, index, total int, envs environments) error {
	p, sideBPkgDir, binA, binB, guardsA, guardsB := prep.pkg, prep.sideBPkgDir, prep.binA, prep.binB, prep.guardsA, prep.guardsB
	// Both sides measure under the pinned environment. Builds use the analysis
	// environment; the reference runtime guard describes this measured one.
	runtimeEnv := envs.measured()
	// -test.benchmem is always on, as for run: allocation deltas ride
	// every comparison without a second measurement (spec §9).
	args := []string{"-test.run=^$", "-test.bench=" + ac.bench, "-test.count=1", "-test.benchmem"}
	if ac.benchtime != "" {
		args = append(args, "-test.benchtime="+ac.benchtime)
	}
	// Interleaved A/B per iteration: block ordering folds slow machine
	// drift (thermal, page cache) into the measured delta; alternation
	// cancels it. Each binary runs from its own tree so cwd-sensitive
	// arms (disk media, testdata) resolve correctly.
	var outA, outB []byte
	throttleBase := ac.snapshotThrottle()
	for i := 0; i < ac.count; i++ {
		// Interruption stops before the next pair; the artifact written
		// so far stands (spec REQ-pew-interruption).
		stopped := func() error {
			return interrupted("ab: %s: interrupted after %d of %d iterations (the artifact holds them)", p.ImportPath, i, ac.count)
		}
		if ctx.Err() != nil {
			return stopped()
		}
		reportPhase(ctx, fmt.Sprintf("comparing %s (%d/%d) iteration %d/%d", p.ImportPath, index, total, i+1, ac.count))
		// A cancellation landing inside a side's process is the
		// interruption too: the process under measurement is killed and
		// the pair in flight is lost, the artifact holding the rest.
		a, err := ac.executeBinary(ctx, p.Dir, ac.pin.List(), runtimeEnv, binA, args)
		if cancelledBy(ctx, err) {
			return stopped()
		}
		if err != nil {
			return fmt.Errorf("ab: side A iteration %d: %w", i+1, err)
		}
		outA = append(outA, a...)
		b, err := ac.executeBinary(ctx, sideBPkgDir, ac.pin.List(), runtimeEnv, binB, args)
		if cancelledBy(ctx, err) {
			return stopped()
		}
		if err != nil {
			return fmt.Errorf("ab: side B iteration %d: %w", i+1, err)
		}
		outB = append(outB, b...)
		// The artifact is written incrementally, one rewrite per
		// completed pair, so an interrupted comparison keeps every
		// iteration it finished (spec REQ-pew-unit-persistence).
		if ac.artifact != nil {
			if err := ac.artifact.pair(p.ImportPath, ac.ref, i+1, a, b); err != nil {
				return err
			}
		}
	}
	throttled := throttleBase.Delta(ac.snapshotThrottle())
	if throttled != nil && *throttled {
		fmt.Fprintf(errw, "pew: warning: thermal throttling occurred during %s A/B measurement\n", p.ImportPath)
		if ac.strict {
			return fmt.Errorf("ab: %s: thermal throttling during measurement (--strict)", p.ImportPath)
		}
	}
	rowsA, corruptA, droppedA, err := run.Parse(outA)
	if err != nil {
		return err
	}
	rowsB, corruptB, droppedB, err := run.Parse(outB)
	if err != nil {
		return err
	}
	for _, dc := range append(droppedA, droppedB...) {
		fmt.Fprintf(errw, "pew: warning: dropping stream configuration key %q (value %q): not a toolchain benchmark key (spec §5)\n", dc.Key, dc.Value)
	}
	for _, cl := range append(corruptA, corruptB...) {
		fmt.Fprintf(errw, "pew: warning: corrupt benchmark output line: %q (%s)\n", cl.Text, cl.Cause)
	}
	if len(rowsA) == 0 || len(rowsB) == 0 {
		return fmt.Errorf("ab: %s: pattern %q produced no results on %s", p.ImportPath, ac.bench, map[bool]string{true: "side A", false: "side B"}[len(rowsA) == 0])
	}
	// The comparator enforces §10.1's guard provenance on both sides; raw
	// go-test streams carry none (Parse refuses reserved keys), so the
	// build-time captures stamp here. Shared process facts (machine,
	// runtime config) satisfy the guards by construction; a genuine
	// per-side build identity difference (toolchain directive, PGO bytes)
	// still refuses.
	for _, cfg := range run.GuardConfig(guardsA) {
		rowsA = withConfig(rowsA, cfg)
	}
	for _, cfg := range run.GuardConfig(guardsB) {
		rowsB = withConfig(rowsB, cfg)
	}
	prep.rows = [2][]*benchfmt.Result{rowsA, rowsB}
	if !ac.jsonOut {
		fmt.Fprintf(w, "pew ab: %s  A=working-tree  B=%s  (%d interleaved iterations)\n", p.ImportPath, ac.ref, ac.count)
	}
	result := compare.Compare(rowsB, rowsA, compare.DefaultOptions())
	var reportErr error
	if ac.jsonOut {
		reportErr = writeStatReportJSON(w, result, result.Coverage(compare.DefaultOptions().Policies))
	} else {
		reportErr = result.WriteText(w)
	}
	if reportErr != nil {
		return reportErr
	}
	if ac.out != "" {
		fmt.Fprintf(errw, "pew: ab artifact written to %s (derivation artifact - never a stat baseline)\n", ac.out)
	}
	return nil
}

// writeABArtifact stores both raw streams as one marked derivation
// artifact. The dirty mark and the pew-ab mark keep it out of every
// stat baseline path by shape (spec §12).
func writeABArtifact(path, importPath, ref string, outA, outB []byte) error {
	a := &abArtifact{path: path}
	return a.pair(importPath, ref, 1, outA, outB)
}

// abPlacement is the directory side B's worktree and both sides' binaries
// are minted in: the repository's parent by default, or the operator's
// --worktree-dir where that parent is unwritable or on another
// filesystem. Either way the placement must share the repository's
// device — a benchmark's media must be side A's — enforced by device
// identity, not assumed from the path shape (a repository on a dedicated
// bench disk is exactly this tool's population); an operator naming a
// different-device directory is refused, never silently degraded, since
// a different medium is the invalid experiment the placement exists to
// prevent (spec §12).
func abPlacement(repoRoot, operatorDir string) (string, error) {
	placement := filepath.Dir(repoRoot)
	named := "the repository parent"
	if operatorDir != "" {
		abs, err := filepath.Abs(operatorDir)
		if err != nil {
			return "", fmt.Errorf("ab: --worktree-dir: %w", err)
		}
		info, err := os.Stat(abs)
		if err != nil {
			return "", fmt.Errorf("ab: --worktree-dir %s: %w", operatorDir, err)
		}
		if !info.IsDir() {
			return "", fmt.Errorf("ab: --worktree-dir %s is not a directory", operatorDir)
		}
		// Inside the repository the placement would dirty the working
		// tree for the run's duration and put the residue sweep's
		// RemoveAll inside it — the sibling default structurally cannot.
		// repoRoot is physical (git's toplevel); resolve the operator's
		// path too, so a symlink into the tree cannot pass the check.
		physical := abs
		if r, err := filepath.EvalSymlinks(abs); err == nil {
			physical = r
		}
		if rel, err := filepath.Rel(repoRoot, physical); err == nil && filepath.IsLocal(rel) {
			return "", fmt.Errorf("ab: --worktree-dir %s lies inside the repository; side B must be placed outside the working tree", operatorDir)
		}
		placement, named = physical, "--worktree-dir "+operatorDir
	}
	same, err := sameDevice(repoRoot, placement)
	if err != nil {
		return "", err
	}
	if !same {
		return "", fmt.Errorf("ab: %s (%s) is on a different filesystem than the repository — side B's media would not match side A's; name a same-filesystem --worktree-dir (spec §12)", named, placement)
	}
	return placement, nil
}

// emptyDir reports whether dir holds no entries at all.
func emptyDir(dir string) bool {
	entries, err := os.ReadDir(dir)
	return err == nil && len(entries) == 0
}

// worktreeOwnedBy reports whether the directory is a linked worktree of
// the repository whose common directory is commonDir: its `.git` file
// names a gitdir under that directory. A directory with no readable
// `.git` file (a crash before `git worktree add` wrote it) is nobody's
// and counts as owned by no repository — never swept on ownership
// grounds alone.
func worktreeOwnedBy(dir, commonDir string) bool {
	data, err := readProfileFile(filepath.Join(dir, ".git"))
	if err != nil {
		return false
	}
	gitdir, ok := strings.CutPrefix(strings.TrimSpace(string(data)), "gitdir:")
	if !ok {
		return false
	}
	gitdir = strings.TrimSpace(gitdir)
	if !filepath.IsAbs(gitdir) {
		gitdir = filepath.Join(dir, gitdir)
	}
	if resolved, err := filepath.EvalSymlinks(gitdir); err == nil {
		gitdir = resolved
	}
	rel, err := filepath.Rel(filepath.Join(commonDir, "worktrees"), gitdir)
	if err != nil || rel == "." || !filepath.IsLocal(rel) || filepath.Base(rel) != rel {
		return false
	}
	if _, err := os.Lstat(gitdir); errors.Is(err, os.ErrNotExist) {
		return true
	} else if err != nil {
		return false
	}
	back, err := readProfileFile(filepath.Join(gitdir, "gitdir"))
	return err == nil && filepath.Clean(strings.TrimSpace(string(back))) == filepath.Join(dir, ".git")
}
