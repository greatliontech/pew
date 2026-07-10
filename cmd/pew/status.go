package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/guard"
	"github.com/greatliontech/pew/internal/gotool"
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
	var staleOnly bool
	cmd := &cobra.Command{
		Use:   "status [packages]",
		Short: "Report each benchmark as valid / stale / unverifiable / unrecorded",
		RunE: func(cmd *cobra.Command, args []string) error {
			patterns := args
			if len(patterns) == 0 {
				patterns = []string{"./..."}
			}
			return runStatus(cmd.OutOrStdout(), benchDir, staleOnly, patterns)
		},
	}
	cmd.Flags().StringVar(&benchDir, "bench-dir", "", "stored-recordings directory (default <module>/benchmarks); an explicit value applies to every package")
	cmd.Flags().BoolVar(&staleOnly, "stale", false, "show only benchmarks that need re-running (non-valid)")
	return cmd
}

type pkgMeta struct {
	ImportPath   string
	Dir          string
	TestGoFiles  []string
	XTestGoFiles []string
	Module       struct {
		Path string
		Dir  string
	}
}

// benchmarkCandidatePaths returns the import paths of in-module packages that have
// test files — the only packages status/run can Compute, since selectedBenchmarks
// reads benchmarks solely from TestGoFiles/XTestGoFiles. This is the batch handed
// to Hasher.Prime: priming a package builds its whole-program SSA up front, so
// priming a package the loop never Computes (no test files ⇒ no selected
// benchmark) is pure waste that would defeat the batch's purpose on a
// sparse-recording tree. A test-bearing package with no recording is still primed
// (a small residual, bounded by the benchmark-bearing package count), because
// whether it has a recording is not known here without reading the store.
func benchmarkCandidatePaths(pkgs []pkgMeta) []string {
	var paths []string
	for _, p := range pkgs {
		if p.Module.Dir != "" && (len(p.TestGoFiles) > 0 || len(p.XTestGoFiles) > 0) {
			paths = append(paths, p.ImportPath)
		}
	}
	return paths
}

// newEngine builds the shared gofresh engine for a command invocation, primed for
// the benchmark-candidate packages and honoring //gofresh:pure directives found in
// them (the durable, in-code purity channel; the recorded per-benchmark pure flag
// is the invocation channel, applied in applyPurity).
func newEngine(pkgs []pkgMeta) (*gofresh.Engine, error) {
	candidates := benchmarkCandidatePaths(pkgs)
	var opts []gofresh.Option
	if len(candidates) > 0 {
		pred, err := gofresh.ScanPureDirectives(candidates...)
		if err != nil {
			return nil, err
		}
		opts = append(opts, gofresh.WithAssumePure(pred))
	}
	e, err := gofresh.New(opts...)
	if err != nil {
		return nil, err
	}
	e.Prime(candidates)
	return e, nil
}

func runStatus(w io.Writer, benchDir string, staleOnly bool, patterns []string) error {
	pkgs, err := resolvePackages(patterns)
	if err != nil {
		return err
	}
	e, err := newEngine(pkgs)
	if err != nil {
		return err
	}
	for _, p := range pkgs {
		if p.Module.Dir == "" {
			continue // not in a module (e.g. a stdlib pattern) — nothing to record
		}
		// A per-package failure (e.g. a sibling that does not compile) is reported
		// as a row and does not abort status of the rest of the tree.
		if err := statusPackage(w, e, benchDir, staleOnly, p); err != nil {
			fmt.Fprintf(w, "%-12s %s  (%v)\n", "error", p.ImportPath, err)
		}
	}
	return nil
}

func statusPackage(w io.Writer, e *gofresh.Engine, benchDir string, staleOnly bool, p pkgMeta) error {
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
	for _, b := range benches {
		v, reason, err := checkOne(st, e, p.ImportPath, pkgRel, p.Module.Dir, b, "")
		if err != nil {
			return err
		}
		if staleOnly && v == verdictValid {
			continue
		}
		line := fmt.Sprintf("%-12s %s.%s", v, p.ImportPath, b)
		if reason != "" {
			line += "  (" + reason + ")"
		}
		fmt.Fprintln(w, line)
	}
	return nil
}

// checkOne is the per-benchmark validity verdict shared by status, run --stale,
// and stat's working-tree staleness warning. The engine recomputes the current
// closure and guards (the SSA load is the dominant cost; an unrecorded benchmark
// needs no analysis, so the store is read first).
func checkOne(st *store.Store, e *gofresh.Engine, pkgPath, pkgRel, moduleDir, bench, label string) (verdict, string, error) {
	recs, err := st.Read(pkgRel, bench, label)
	switch {
	case errors.Is(err, store.ErrNotRecorded):
		return verdictUnrecorded, "", nil
	case err != nil:
		return "", "", err
	}
	fp, pure := fingerprintFromConfig(recs[0].Config)
	v, err := e.Check(fp, gofresh.Subject{Package: pkgPath, Symbol: bench}, moduleDir, gofresh.Measurement)
	if err != nil {
		return "", "", err
	}
	v = applyPurity(v, pure)
	return verdict(v.Status), v.Reason, nil
}

// fingerprintFromConfig reads the recorded fingerprint out of a recording's config
// lines (spec §5: pew owns the serialization, gofresh owns the semantics), plus the
// recorded per-benchmark purity flag ("" when none).
func fingerprintFromConfig(cfg []benchfmt.Config) (gofresh.Fingerprint, string) {
	m := make(map[string]string, len(cfg))
	for _, c := range cfg {
		m[c.Key] = string(c.Value)
	}
	return gofresh.Fingerprint{
		Closure: m["pew-closure"],
		Guards: guard.Guards{
			Toolchain:     m["toolchain"],
			BuildConfig:   m["buildconfig"],
			Machine:       m["machine"],
			RuntimeConfig: m["runtimeconfig"],
		},
		RuntimeInputs: m["pew-runtime-inputs"],
		RuntimeDigest: m["pew-runtime"],
	}, m["pure"]
}

// applyPurity folds the recorded per-benchmark purity flag into the engine verdict
// (spec §7.3, §7.5). --impure (pure:false) declares external state: the benchmark
// always re-runs unless a guard already staled it, so any non-stale verdict becomes
// unverifiable "impure". --assume-pure (pure:true) is the author suppressing the
// remaining unverifiability after every hashable guard held, so unverifiable
// becomes valid. The in-code //gofresh:pure directive channel is applied inside the
// engine itself (newEngine).
func applyPurity(v gofresh.Verdict, pure string) gofresh.Verdict {
	switch pure {
	case "false":
		if v.Status != gofresh.Stale {
			return gofresh.Verdict{Status: gofresh.Unverifiable, Reason: "impure"}
		}
	case "true":
		if v.Status == gofresh.Unverifiable {
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
