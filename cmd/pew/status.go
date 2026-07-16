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

// newEngine roots one immutable Gofresh configuration at a module. Views discover
// purity directives from their own selected source.
func newEngine(moduleDir string) (*gofresh.Engine, error) {
	return gofresh.New(gofresh.WithDir(moduleDir))
}

func newEngineWithEnv(moduleDir string, env []string) (*gofresh.Engine, error) {
	return gofresh.New(gofresh.WithDir(moduleDir), gofresh.WithEnv(env...))
}

func runStatus(w io.Writer, benchDir string, staleOnly bool, patterns []string) error {
	pkgs, err := resolvePackages(patterns)
	if err != nil {
		return err
	}
	for _, p := range pkgs {
		if p.Module.Dir == "" {
			continue // not in a module (e.g. a stdlib pattern) — nothing to record
		}
		e, err := newEngine(p.Module.Dir)
		if err != nil {
			return err
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
	if !store.IsRecordingShape(recs) {
		return verdictStale, "format", nil
	}
	fp, pure, ok := fingerprintFromConfig(recs[0].Config)
	if !ok {
		return verdictStale, "format", nil
	}
	v, err := e.Check(fp, gofresh.Subject{Package: pkgPath, Symbol: bench}, moduleDir)
	if err != nil {
		return "", "", err
	}
	v = applyPurity(v, pure)
	return verdict(v.Status), v.Reason, nil
}

// fingerprintFromConfig reads the recorded fingerprint out of a recording's config
// lines (spec §5: pew owns the serialization, gofresh owns the semantics), plus the
// recorded per-benchmark purity flag ("" when none).
func fingerprintFromConfig(cfg []benchfmt.Config) (gofresh.Fingerprint, string, bool) {
	m := make(map[string]string, len(cfg))
	formatCount := 0
	for _, c := range cfg {
		m[c.Key] = string(c.Value)
		if c.Key == "pew-format" {
			formatCount++
		}
	}
	if m["pew-format-invalid"] == "true" || formatCount != 1 || m["pew-format"] != runpkg.RecordingFormat {
		return gofresh.Fingerprint{}, "", false
	}
	return gofresh.Fingerprint{
		MaximalClosure: m["pew-closure"],
		Guards: guard.Guards{
			Toolchain:     m["toolchain"],
			BuildConfig:   m["buildconfig"],
			Machine:       m["machine"],
			RuntimeConfig: m["runtimeconfig"],
		},
		PurityAssertion: m["pew-purity"],
		RuntimeInputs:   m["pew-runtime-inputs"],
		RuntimeDigest:   m["pew-runtime"],
		ResultKind:      gofresh.Measurement,
	}, m["pure"], true
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
