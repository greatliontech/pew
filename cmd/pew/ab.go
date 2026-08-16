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
	benchmem  bool
	ref       string
	pin       string
	strict    bool
	out       string
	throttle  func() run.ThrottleSnapshot
	execute   func(dir, pin string, env []string, bin string, args []string) ([]byte, error)
	build     func(dir string, env []string, args []string) error
	guards    func(moduleDir, pkgDir string, mainPkg bool, env []string) (guard.Guards, error)
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

func (ac abConfig) executeBinary(dir, pin string, env []string, bin string, args []string) ([]byte, error) {
	if ac.execute != nil {
		return ac.execute(dir, pin, env, bin, args)
	}
	return run.ExecuteBinary(dir, pin, env, bin, args)
}

func (ac abConfig) buildBinary(dir string, env []string, args []string) error {
	if ac.build != nil {
		return ac.build(dir, env, args)
	}
	cmd := exec.Command("go", args...)
	resolved := gotool.CommandDir(dir)
	cmd.Dir = resolved
	cmd.Env = gotool.CommandEnvironment(env, resolved)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ab: go %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func newABCmd() *cobra.Command {
	ac := abConfig{}
	cmd := &cobra.Command{
		Use:   "ab [packages]",
		Short: "A/B-compare the working tree against a ref without touching either",
		Long: "Benchmarks the uncommitted working tree (side A) against a ref (side B,\n" +
			"default HEAD) materialized in a temporary git worktree: both sides build\n" +
			"first, runs interleave A/B per iteration so machine drift cancels instead\n" +
			"of folding into the delta, and the repository stays writable throughout —\n" +
			"no stash cycle, no tree mutation, crash-safe by construction. The verdict\n" +
			"uses the same significance machinery as stat. Output is a derivation\n" +
			"artifact, never a stat baseline: nothing is written to the recording\n" +
			"store (spec §12).",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAB(cmd.OutOrStdout(), cmd.ErrOrStderr(), ac, args)
		},
	}
	f := cmd.Flags()
	f.StringVar(&ac.bench, "bench", ".", "benchmark pattern (go test -bench syntax)")
	f.IntVar(&ac.count, "count", 6, "interleaved iterations per side")
	f.StringVar(&ac.benchtime, "benchtime", "", "per-benchmark time or iteration budget (go test -benchtime)")
	f.BoolVar(&ac.benchmem, "benchmem", false, "capture allocation statistics per side")
	f.StringVar(&ac.ref, "ref", "HEAD", "B side: any git rev the repository resolves")
	f.StringVar(&ac.pin, "pin", "", "CPU list for taskset pinning, both sides")
	f.BoolVar(&ac.strict, "strict", false, "refuse to measure under noisy machine conditions")
	f.StringVar(&ac.out, "out", "", "also write both sides' raw benchmark streams to this file (a derivation artifact, never a stat baseline)")
	return cmd
}

func runAB(w, errw io.Writer, ac abConfig, patterns []string) error {
	if ac.count < 1 {
		return fmt.Errorf("ab: count must be at least 1")
	}
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
	// B side: the ref materialized in a disposable worktree - never a
	// stash, never a mutation of the working tree; a crash leaves a
	// removable directory and a writable repository.
	worktree, cleanup, err := addWorktree(repoRoot, ac.ref)
	if err != nil {
		return err
	}
	defer cleanup()
	env := os.Environ()
	for _, p := range pkgs {
		// Each package's own module maps into the worktree - a go.work
		// pattern can resolve packages from several modules, and a
		// module outside this repository has no B side to compare.
		moduleRel, err := filepath.Rel(repoRoot, p.Module.Dir)
		if err != nil || strings.HasPrefix(moduleRel, "..") {
			return fmt.Errorf("ab: package %s lives in a module outside this repository (%s)", p.ImportPath, p.Module.Dir)
		}
		if err := abPackage(w, errw, ac, p, repoRoot, worktree, moduleRel, env); err != nil {
			return err
		}
	}
	return nil
}

func abPackage(w, errw io.Writer, ac abConfig, p pkgMeta, repoRoot, worktree, moduleRel string, env []string) error {
	pkgRelToModule, err := filepath.Rel(p.Module.Dir, p.Dir)
	if err != nil {
		return err
	}
	sideBModule := filepath.Join(worktree, moduleRel)
	sideBPkgDir := filepath.Join(sideBModule, pkgRelToModule)
	if _, err := os.Stat(sideBPkgDir); err != nil {
		return fmt.Errorf("ab: package %s does not exist at %s: %w", p.ImportPath, ac.ref, err)
	}
	// Both sides build BEFORE either side measures: two standing
	// binaries make interleaving free, and the shared build cache makes
	// the second build cheap.
	tmp, err := os.MkdirTemp("", "pew-ab-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	binA := filepath.Join(tmp, "a.test")
	binB := filepath.Join(tmp, "b.test")
	// Builds run at each side's MODULE root with a relative package
	// target, exactly as the recording path builds: a relative -pgo in
	// GOFLAGS resolves against the build cwd, and the guard digest pins
	// the module-root resolution — building anywhere else lets the
	// compile consume bytes the guard never digested.
	relTarget := "."
	if pkgRelToModule != "." {
		relTarget = "./" + filepath.ToSlash(pkgRelToModule)
	}
	if err := ac.buildBinary(p.Module.Dir, env, []string{"test", "-c", "-o", binA, relTarget}); err != nil {
		return fmt.Errorf("ab: building side A (working tree): %w", err)
	}
	if err := ac.buildBinary(sideBModule, env, []string{"test", "-c", "-o", binB, relTarget}); err != nil {
		return fmt.Errorf("ab: building side B (%s): %w", ac.ref, err)
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
		return fmt.Errorf("ab: resolving side B package at %s: %w", ac.ref, err)
	}
	guardsA, err := ac.sideGuards(p.Module.Dir, p.Dir, p.Name == "main", env)
	if err != nil {
		return fmt.Errorf("ab: capturing side A guards: %w", err)
	}
	guardsB, err := ac.sideGuards(sideBModule, sideBPkgDir, nameB == "main", env)
	if err != nil {
		return fmt.Errorf("ab: capturing side B guards: %w", err)
	}
	args := []string{"-test.run=^$", "-test.bench=" + ac.bench, "-test.count=1"}
	if ac.benchtime != "" {
		args = append(args, "-test.benchtime="+ac.benchtime)
	}
	if ac.benchmem {
		args = append(args, "-test.benchmem")
	}
	// Interleaved A/B per iteration: block ordering folds slow machine
	// drift (thermal, page cache) into the measured delta; alternation
	// cancels it. Each binary runs from its own tree so cwd-sensitive
	// arms (disk media, testdata) resolve correctly.
	var outA, outB []byte
	throttleBase := ac.snapshotThrottle()
	for i := 0; i < ac.count; i++ {
		a, err := ac.executeBinary(p.Dir, ac.pin, env, binA, args)
		if err != nil {
			return fmt.Errorf("ab: side A iteration %d: %w", i+1, err)
		}
		outA = append(outA, a...)
		b, err := ac.executeBinary(sideBPkgDir, ac.pin, env, binB, args)
		if err != nil {
			return fmt.Errorf("ab: side B iteration %d: %w", i+1, err)
		}
		outB = append(outB, b...)
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
		if err := writeABArtifact(ac.out, p.ImportPath, ac.ref, outA, outB); err != nil {
			return err
		}
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
	return os.WriteFile(path, []byte(b.String()), 0o644)
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
// writable throughout. The worktree is created BESIDE the repository —
// same filesystem — never in the OS temp dir: a benchmark keeping its
// media package-dir-relative (the durable-write arms) measures that
// filesystem's storage, and an os.TempDir worktree on a tmpfs host
// hands side B RAM-backed fsyncs while side A pays the disk, an
// invalid experiment no interleaving can rescue. An unwritable parent
// is a hard error, not a silent fallback to a different medium.
func addWorktree(repoRoot, ref string) (string, func(), error) {
	dir, err := os.MkdirTemp(filepath.Dir(repoRoot), ".pew-ab-worktree-*")
	if err != nil {
		return "", nil, fmt.Errorf("ab: creating the side-B worktree beside the repository (same filesystem, spec §12): %w", err)
	}
	// Sibling placement is same-filesystem only when the repository is
	// not itself a mount boundary (a repo on a dedicated bench disk is
	// exactly this tool's population) — so the contract is enforced by
	// device identity, not assumed from the path shape.
	if same, err := sameDevice(repoRoot, dir); err != nil {
		os.RemoveAll(dir)
		return "", nil, err
	} else if !same {
		os.RemoveAll(dir)
		return "", nil, fmt.Errorf("ab: the repository parent %s is on a different filesystem than the repository — side B's media would not match side A's (spec §12)", filepath.Dir(repoRoot))
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
