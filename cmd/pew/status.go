package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/thegrumpylion/pew/internal/closure"
	"github.com/thegrumpylion/pew/internal/gotool"
	"github.com/thegrumpylion/pew/internal/provenance"
	"github.com/thegrumpylion/pew/internal/runtimeinputs"
	"github.com/thegrumpylion/pew/internal/stale"
	"github.com/thegrumpylion/pew/internal/store"
	"golang.org/x/perf/benchfmt"
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

func runStatus(w io.Writer, benchDir string, staleOnly bool, patterns []string) error {
	pkgs, err := resolvePackages(patterns)
	if err != nil {
		return err
	}
	h, err := closure.New()
	if err != nil {
		return err
	}
	for _, p := range pkgs {
		if p.Module.Dir == "" {
			continue // not in a module (e.g. a stdlib pattern) — nothing to record
		}
		// A per-package failure (e.g. a sibling that does not compile) is reported
		// as a row and does not abort status of the rest of the tree.
		if err := statusPackage(w, h, benchDir, staleOnly, p); err != nil {
			fmt.Fprintf(w, "%-12s %s  (%v)\n", "error", p.ImportPath, err)
		}
	}
	return nil
}

func statusPackage(w io.Writer, h *closure.Hasher, benchDir string, staleOnly bool, p pkgMeta) error {
	benches, err := selectedBenchmarks(p)
	if err != nil {
		return err
	}
	if len(benches) == 0 {
		return nil
	}
	prov, err := provenance.Capture(p.Module.Dir)
	if err != nil {
		return err
	}
	dir := benchDir
	if dir == "" {
		dir = filepath.Join(p.Module.Dir, "benchmarks")
	}
	st := store.New(dir)
	pkgRel := strings.TrimPrefix(strings.TrimPrefix(p.ImportPath, p.Module.Path), "/")
	for _, b := range benches {
		verdict, reason, err := checkOne(st, h, p.ImportPath, pkgRel, p.Module.Dir, b, "", prov)
		if err != nil {
			return err
		}
		if staleOnly && verdict == stale.Valid {
			continue
		}
		line := fmt.Sprintf("%-12s %s.%s", verdict, p.ImportPath, b)
		if reason != "" {
			line += "  (" + reason + ")"
		}
		fmt.Fprintln(w, line)
	}
	return nil
}

// checkOne is the per-benchmark validity verdict shared by status, run --stale,
// and stat's working-tree staleness warning. The HEAD closure is computed only
// when a recording exists (the SSA load is the dominant cost, §7.4; an unrecorded
// benchmark needs no analysis). The Tier-2 closure is per benchmark, so it is
// computed here in the per-benchmark loop, not once per package.
func checkOne(st *store.Store, h *closure.Hasher, pkgPath, pkgRel, moduleDir, bench, label string, prov provenance.Provenance) (stale.Verdict, string, error) {
	recs, err := st.Read(pkgRel, bench, label)
	switch {
	case errors.Is(err, store.ErrNotRecorded):
		return stale.Unrecorded, "", nil
	case err != nil:
		return "", "", err
	default:
		cl, err := h.Compute(pkgPath, bench)
		if err != nil {
			return "", "", err
		}
		runtimeState := currentRuntimeState(recs[0].Config, moduleDir)
		v, reason := stale.Check(prov, cl, runtimeState, recs[0].Config)
		return v, reason, nil
	}
}

func currentRuntimeState(cfg []benchfmt.Config, moduleDir string) stale.RuntimeState {
	manifest := ""
	for _, c := range cfg {
		if c.Key == "pew-runtime-inputs" {
			manifest = string(c.Value)
			break
		}
	}
	if manifest == "" {
		return stale.RuntimeState{}
	}
	cur, err := runtimeinputs.Current(manifest, moduleDir)
	if err != nil {
		return stale.RuntimeState{}
	}
	return stale.RuntimeState{Digest: cur.Digest, Unverifiable: cur.Unverifiable, Reason: cur.Reason, OK: cur.OK}
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
