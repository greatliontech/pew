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
	"github.com/thegrumpylion/pew/internal/stale"
	"github.com/thegrumpylion/pew/internal/store"
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
				patterns = []string{"."}
			}
			return runStatus(cmd.OutOrStdout(), benchDir, staleOnly, patterns)
		},
	}
	cmd.Flags().StringVar(&benchDir, "bench-dir", "", "stored-recordings directory (default <module>/benchmarks); an explicit value applies to every package")
	cmd.Flags().BoolVar(&staleOnly, "stale", false, "show only benchmarks that need re-running (non-valid)")
	return cmd
}

type pkgMeta struct {
	ImportPath string
	Module     struct {
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
	benches, err := stale.ListBenchmarks(p.ImportPath)
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
	curClosure, err := h.Hash(p.ImportPath)
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
		verdict, reason, err := checkOne(st, pkgRel, b, prov, curClosure)
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

func checkOne(st *store.Store, pkgRel, bench string, prov provenance.Provenance, curClosure string) (stale.Verdict, string, error) {
	recs, err := st.Read(pkgRel, bench, "")
	switch {
	case errors.Is(err, store.ErrNotRecorded):
		return stale.Unrecorded, "", nil
	case err != nil:
		return "", "", err
	default:
		v, reason := stale.Check(prov, curClosure, recs[0].Config)
		return v, reason, nil
	}
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
