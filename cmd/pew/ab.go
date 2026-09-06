package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/greatliontech/gofresh/guard"
	"github.com/spf13/cobra"

	"github.com/greatliontech/pew/internal/compare"
	"github.com/greatliontech/pew/internal/gotool"
	"github.com/greatliontech/pew/internal/run"
)

// abConfig carries pew ab's seams, mirroring runConfig's: tests observe
// build/execute ordering without real toolchain work.
type abConfig struct {
	bench     string
	count     int
	benchtime string
	ref       string
	pin       run.Pin // the derived CPU set both sides run on; unpinned when empty
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
	guards      func(moduleDir, pkgDir string, mainPkg bool, env []string) (guard.Guards, error)
}

// sideGuards captures one side's comparison-guard values in that side's own
// tree: the toolchain and build-config guards are the side's build identity
// (a ref pinning a different toolchain directive, or shipping a different
// PGO profile, is a genuine mismatch the comparator must refuse), while the
// machine and runtime-config guards are process facts the two sides share by
// construction. The PGO profile rides in as a content digest exactly as the
// recording path's engine takes it.
func (ac abConfig) sideGuards(moduleDir, pkgDir string, mainPkg bool, env []string) (guard.Guards, error) {
	if ac.guards != nil {
		return ac.guards(moduleDir, pkgDir, mainPkg, env)
	}
	goflags, err := run.EffectiveGoflags(moduleDir, env)
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
	return guard.CaptureForContextEnv(context.Background(), moduleDir, env, guard.Measurement, buildInputs...)
}

// abPackageName resolves a package directory's package name with the same
// toolchain environment the builds use.
func abPackageName(dir string, env []string) (string, error) {
	cmd := exec.Command("go", "list", "-f", "{{.Name}}", ".")
	resolved := gotool.CommandDir(dir)
	cmd.Dir = resolved
	cmd.Env = gotool.CommandEnvironment(env, resolved)
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			return "", fmt.Errorf("go list: %w: %s", err, strings.TrimSpace(string(ee.Stderr)))
		}
		return "", fmt.Errorf("go list: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (ac abConfig) snapshotThrottle() run.ThrottleSnapshot {
	if ac.throttle != nil {
		return ac.throttle()
	}
	return run.SnapshotThrottle()
}

func (ac abConfig) executeBinary(ctx context.Context, dir, pin string, env []string, bin string, args []string) ([]byte, error) {
	if ac.execute != nil {
		return ac.execute(dir, pin, env, bin, args)
	}
	return run.ExecuteBinaryContext(ctx, dir, pin, env, bin, args)
}

func (ac abConfig) buildBinary(ctx context.Context, dir string, env []string, args []string) error {
	if ac.build != nil {
		return ac.build(dir, env, args)
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
	f.StringVar(&ac.bench, "bench", ".", "benchmark pattern (go test -bench syntax)")
	f.IntVar(&ac.count, "count", 6, "interleaved iterations per side")
	f.StringVar(&ac.benchtime, "benchtime", "", "per-benchmark time or iteration budget (go test -benchtime)")
	f.StringVar(&ac.ref, "ref", "HEAD", "B side: any git rev the repository resolves")
	f.BoolVar(&pin, "pin", false, "pin both sides to one CPU set derived from the host's topology (taskset)")
	f.StringVar(&ac.worktreeDir, "worktree-dir", "", "same-filesystem directory for side B's worktree and both binaries when the repository's parent is unwritable or on another filesystem (default: the repository's parent)")
	f.BoolVar(&ac.strict, "strict", false, "refuse to measure under noisy machine conditions")
	f.StringVar(&ac.out, "out", "", "also write both sides' raw benchmark streams to this file (a derivation artifact, never a stat baseline)")
	return cmd
}

func runAB(ctx context.Context, w, errw io.Writer, ac abConfig, patterns []string) error {
	if ac.count < 1 {
		return fmt.Errorf("ab: count must be at least 1")
	}
	reportPhase("listing")
	pkgs, err := resolvePackages(patterns)
	if err != nil {
		return err
	}
	if len(pkgs) == 0 {
		return fmt.Errorf("ab: no packages matched")
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
	repoRoot, err := gitTopLevel(moduleDir)
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
	for _, swept := range sweepStaleWorktrees(repoRoot, placement) {
		fmt.Fprintf(errw, "pew: swept a stale side-B worktree %s (a killed run's residue)\n", swept)
	}
	// B side: the ref materialized in a disposable worktree - never a
	// stash, never a mutation of the working tree; a crash leaves a
	// removable directory and a writable repository.
	worktree, cleanup, err := addWorktree(repoRoot, placement, ac.ref)
	if err != nil {
		return err
	}
	defer cleanup()
	envs := newEnvironments(os.Environ(), ac.pin)
	env := envs.analysis
	// Every package prepares before any package measures (spec
	// REQ-pew-preparation): containment, the B side's existence, both
	// trees' benchmark declarations against the pattern, both builds,
	// both guard captures, and the guard comparison — so a later
	// package's refusal is known before an earlier package spends its
	// 2 × count iterations, and a guard mismatch never reaches a first
	// iteration.
	var preps []*abPreparation
	for i, p := range pkgs {
		if err := ctx.Err(); err != nil {
			return interrupted("ab: interrupted while preparing %s (package %d/%d); nothing compared", p.ImportPath, i+1, len(pkgs))
		}
		reportPhase(fmt.Sprintf("preparing %s (%d/%d)", p.ImportPath, i+1, len(pkgs)))
		prep, err := prepareABPackage(ctx, ac, p, repoRoot, worktree, placement, env)
		if prep != nil && prep.tmp != "" {
			defer os.RemoveAll(prep.tmp)
		}
		if err != nil {
			return err
		}
		preps = append(preps, prep)
	}
	for i, prep := range preps {
		if err := ctx.Err(); err != nil {
			return interrupted("ab: interrupted before %s (%d of %d packages compared)", prep.pkg.ImportPath, i, len(preps))
		}
		if err := abPackage(ctx, w, errw, ac, prep, i+1, len(preps), envs); err != nil {
			return err
		}
	}
	return nil
}

// abPreparation is one package's A/B preparation record: both sides
// located, built, and guard-captured, the guards agreeing on every
// comparison key, before the first iteration.
type abPreparation struct {
	pkg              pkgMeta
	sideBPkgDir      string
	tmp, binA, binB  string
	guardsA, guardsB guard.Guards
}

// prepareABPackage builds a package's A/B preparation record, firing
// every refusal the two trees decide before any iteration; a returned
// record with a temp dir owns it even when the error is non-nil.
func prepareABPackage(ctx context.Context, ac abConfig, p pkgMeta, repoRoot, worktree, placement string, env []string) (*abPreparation, error) {
	// Each package's own module maps into the worktree - a go.work
	// pattern can resolve packages from several modules, and a
	// module outside this repository has no B side to compare.
	moduleRel, err := filepath.Rel(repoRoot, p.Module.Dir)
	if err != nil || strings.HasPrefix(moduleRel, "..") {
		return nil, fmt.Errorf("ab: package %s lives in a module outside this repository (%s)", p.ImportPath, p.Module.Dir)
	}
	pkgRelToModule, err := filepath.Rel(p.Module.Dir, p.Dir)
	if err != nil {
		return nil, err
	}
	sideBModule := filepath.Join(worktree, moduleRel)
	sideBPkgDir := filepath.Join(sideBModule, pkgRelToModule)
	if _, err := os.Stat(sideBPkgDir); err != nil {
		return nil, fmt.Errorf("ab: package %s does not exist at %s: %w", p.ImportPath, ac.ref, err)
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
	nameB, err := abPackageName(sideBPkgDir, env)
	if err != nil {
		return prep, fmt.Errorf("ab: resolving side B package at %s: %w", ac.ref, err)
	}
	prep.guardsA, err = ac.sideGuards(p.Module.Dir, p.Dir, p.Name == "main", env)
	if err != nil {
		return prep, fmt.Errorf("ab: capturing side A guards: %w", err)
	}
	prep.guardsB, err = ac.sideGuards(sideBModule, sideBPkgDir, nameB == "main", env)
	if err != nil {
		return prep, fmt.Errorf("ab: capturing side B guards: %w", err)
	}
	// A guard mismatch is refused HERE, before the first iteration (spec
	// §12): the comparator would only note it after the whole
	// measurement, which is the spend the refusal exists to save.
	if err := abGuardsAgree(p.ImportPath, ac.ref, prep.guardsA, prep.guardsB); err != nil {
		return prep, err
	}
	return prep, nil
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
// on any comparison guard — the four keys the measurement comparison
// judges (run.GuardConfig's order: toolchain, buildconfig, machine,
// runtimeconfig) — naming the first differing guard and both values.
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
	// Both sides measure under the pinned environment; the builds and
	// guard captures stayed on the analysis one (the guards are compared
	// between the sides, never recorded).
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
		if ctx.Err() != nil {
			return interrupted("ab: %s: interrupted after %d of %d iterations (the artifact holds them)", p.ImportPath, i, ac.count)
		}
		reportPhase(fmt.Sprintf("comparing %s (%d/%d) iteration %d/%d", p.ImportPath, index, total, i+1, ac.count))
		a, err := ac.executeBinary(ctx, p.Dir, ac.pin.List(), runtimeEnv, binA, args)
		if err != nil {
			return fmt.Errorf("ab: side A iteration %d: %w", i+1, err)
		}
		outA = append(outA, a...)
		b, err := ac.executeBinary(ctx, sideBPkgDir, ac.pin.List(), runtimeEnv, binB, args)
		if err != nil {
			return fmt.Errorf("ab: side B iteration %d: %w", i+1, err)
		}
		outB = append(outB, b...)
		// The artifact is written incrementally, one rewrite per
		// completed pair, so an interrupted comparison keeps every
		// iteration it finished (spec REQ-pew-unit-persistence).
		if ac.out != "" {
			if err := writeABArtifact(ac.out, p.ImportPath, ac.ref, outA, outB); err != nil {
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
	fmt.Fprintf(w, "pew ab: %s  A=working-tree  B=%s  (%d interleaved iterations)\n", p.ImportPath, ac.ref, ac.count)
	result := compare.Compare(rowsB, rowsA, compare.DefaultOptions())
	if err := result.WriteText(w); err != nil {
		return err
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
	var b strings.Builder
	b.WriteString("pew-ab: 1\n")
	b.WriteString("dirty: true\n")
	b.WriteString("pkg: " + importPath + "\n")
	b.WriteString("pew-ab-ref: " + ref + "\n\n")
	b.WriteString("pew-ab-side: A\n")
	b.Write(outA)
	b.WriteString("\npew-ab-side: B\n")
	b.Write(outB)
	// Rewritten per completed iteration: the replacement is atomic so a
	// reader never sees a torn artifact.
	tmp, err := os.CreateTemp(filepath.Dir(path), ".pew-ab-out-*")
	if err != nil {
		return err
	}
	if _, err := tmp.WriteString(b.String()); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	// The artifact keeps the mode a plain write would give it, not the
	// temp file's private one.
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func gitTopLevel(dir string) (string, error) {
	cmd := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("ab: not inside a git repository: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// addWorktree materializes ref in a disposable detached worktree and
// returns its path with a cleanup that removes it; the repository stays
// writable throughout. The worktree is created in the placement — the
// repository's own parent by default, or the operator's --worktree-dir
// — on the repository's filesystem, never in the OS temp dir: a
// benchmark keeping its media package-dir-relative (the durable-write
// arms) measures that filesystem's storage, and an os.TempDir worktree
// on a tmpfs host hands side B RAM-backed fsyncs while side A pays the
// disk, an invalid experiment no interleaving can rescue. An
// unwritable placement is a hard error, not a silent fallback to a
// different medium.
func addWorktree(repoRoot, placement, ref string) (string, func(), error) {
	dir, err := os.MkdirTemp(placement, ".pew-ab-worktree-*")
	if err != nil {
		return "", nil, fmt.Errorf("ab: creating the side-B worktree in %s (the repository's filesystem, spec §12): %w", placement, err)
	}
	cmd := exec.Command("git", "-C", repoRoot, "worktree", "add", "--detach", dir, ref)
	if out, err := cmd.CombinedOutput(); err != nil {
		os.RemoveAll(dir)
		return "", nil, fmt.Errorf("ab: git worktree add %s: %w: %s", ref, err, strings.TrimSpace(string(out)))
	}
	cleanup := func() {
		remove := exec.Command("git", "-C", repoRoot, "worktree", "remove", "--force", dir)
		if err := remove.Run(); err != nil {
			os.RemoveAll(dir)
			_ = exec.Command("git", "-C", repoRoot, "worktree", "prune").Run()
		}
	}
	return dir, cleanup, nil
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
		placement, named = abs, "--worktree-dir "+operatorDir
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

// sweepStaleWorktrees removes the `.pew-ab-worktree-*` directories in the
// placement that THIS repository minted — its `.git` file's gitdir lies
// under the repository's common directory, or the directory is empty, a
// mint git never populated — and that `git worktree list` no longer
// registers: a killed run's residue, self-healed on the next
// run instead of accumulating beside the repository, then the stale
// registrations pruned, the cross-check `git worktree prune` itself
// trusts. A registered worktree (a run in flight) is never touched, and
// neither is a worktree another repository owns: sibling repositories
// share the default placement, and a shared --worktree-dir is
// legitimate, so ownership is read from the residue itself, never
// inferred from the placement. Returns the swept directories.
func sweepStaleWorktrees(repoRoot, placement string) []string {
	entries, err := os.ReadDir(placement)
	if err != nil {
		return nil
	}
	commonDir, err := gitCommonDir(repoRoot)
	if err != nil {
		return nil
	}
	registered := map[string]bool{}
	if out, err := exec.Command("git", "-C", repoRoot, "worktree", "list", "--porcelain").Output(); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			if path, ok := strings.CutPrefix(line, "worktree "); ok {
				if resolved, err := filepath.EvalSymlinks(path); err == nil {
					path = resolved
				}
				registered[path] = true
			}
		}
	} else {
		// Without the registry no residue can be told from a run in
		// flight: sweep nothing.
		return nil
	}
	var swept []string
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), ".pew-ab-worktree-") {
			continue
		}
		path := filepath.Join(placement, entry.Name())
		// This repository's residue, or an empty directory — a run
		// killed between minting the directory and git writing its
		// .git file leaves the latter; empty, it can be nobody's live
		// worktree (a sibling's in-flight mint is re-created by its own
		// `git worktree add`). Anything else is another repository's.
		if !worktreeOwnedBy(path, commonDir) && !emptyDir(path) {
			continue
		}
		resolved := path
		if r, err := filepath.EvalSymlinks(path); err == nil {
			resolved = r
		}
		if registered[resolved] {
			continue
		}
		if err := os.RemoveAll(path); err == nil {
			swept = append(swept, path)
		}
	}
	if len(swept) > 0 {
		_ = exec.Command("git", "-C", repoRoot, "worktree", "prune").Run()
	}
	return swept
}

// gitCommonDir is the repository's common git directory, resolved —
// the one directory every worktree the repository owns points into.
func gitCommonDir(repoRoot string) (string, error) {
	out, err := exec.Command("git", "-C", repoRoot, "rev-parse", "--git-common-dir").Output()
	if err != nil {
		return "", err
	}
	dir := strings.TrimSpace(string(out))
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(repoRoot, dir)
	}
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	return dir, nil
}

// worktreeOwnedBy reports whether the directory is a linked worktree of
// the repository whose common directory is commonDir: its `.git` file
// names a gitdir under that directory. A directory with no readable
// `.git` file (a crash before `git worktree add` wrote it) is nobody's
// and counts as owned by no repository — never swept on ownership
// grounds alone.
// emptyDir reports whether dir holds no entries at all.
func emptyDir(dir string) bool {
	entries, err := os.ReadDir(dir)
	return err == nil && len(entries) == 0
}

func worktreeOwnedBy(dir, commonDir string) bool {
	data, err := os.ReadFile(filepath.Join(dir, ".git"))
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
	rel, err := filepath.Rel(commonDir, gitdir)
	return err == nil && filepath.IsLocal(rel)
}
