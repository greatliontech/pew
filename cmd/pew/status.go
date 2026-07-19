package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
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
	var label string
	var staleOnly bool
	var explain bool
	var jsonOut bool
	cmd := &cobra.Command{
		Use:   "status [packages]",
		Short: "Report each benchmark as valid / stale / unverifiable / unrecorded",
		RunE: func(cmd *cobra.Command, args []string) error {
			if explain && jsonOut {
				return fmt.Errorf("status: --explain and -json are mutually exclusive (the explanation is a human view)")
			}
			patterns := args
			if len(patterns) == 0 {
				patterns = []string{"./..."}
			}
			return runStatus(cmd.OutOrStdout(), benchDir, label, staleOnly, explain, jsonOut, patterns)
		},
	}
	cmd.Flags().StringVar(&benchDir, "bench-dir", "", "stored-recordings directory (default <module>/benchmarks); an explicit value applies to every package")
	cmd.Flags().StringVar(&label, "label", "", "variant label to check (spec §6); empty = the unlabeled recording")
	cmd.Flags().BoolVar(&staleOnly, "stale", false, "show only benchmarks that need re-running (non-valid)")
	cmd.Flags().BoolVar(&explain, "explain", false, "explain each non-valid verdict: recorded vs current guard/input values (spec §12)")
	cmd.Flags().BoolVar(&jsonOut, "json", false, "emit one JSON object per row (spec §12, --json)")
	return cmd
}

type pkgMeta struct {
	ImportPath   string
	Name         string
	Dir          string
	TestGoFiles  []string
	XTestGoFiles []string
	Module       struct {
		Path string
		Dir  string
	}
}

// newEngineForPkg roots one immutable Gofresh configuration at a package's
// module. Views discover purity directives from their own selected source. The
// package's effective PGO profile — explicit -pgo in the effective GOFLAGS, or
// a tested main package's default.pgo — rides in as a content-digest build
// input, so the buildconfig guard moves when the profile's bytes do, not
// merely when the flag string does (spec §5/§9); a profile the compile will
// consume but pew cannot read fails closed here. The resolved input is
// returned beside the engine so the producer can revalidate it before writing.
func newEngineForPkg(p pkgMeta, env []string) (*gofresh.Engine, string, error) {
	return newEngineAt(p.Module.Dir, p.Dir, p.Name == "main", env)
}

func newEngineAt(moduleDir, pkgDir string, mainPkg bool, env []string) (*gofresh.Engine, string, error) {
	goflags, err := runpkg.EffectiveGoflags(moduleDir, env)
	if err != nil {
		return nil, "", err
	}
	pgo, err := runpkg.PGOInput(moduleDir, pkgDir, mainPkg, goflags)
	if err != nil {
		return nil, "", err
	}
	e, err := buildEngine(moduleDir, env, pgo)
	return e, pgo, err
}

func buildEngine(moduleDir string, env []string, pgo string) (*gofresh.Engine, error) {
	opts := []gofresh.Option{gofresh.WithDir(moduleDir), gofresh.WithEnv(env...)}
	if pgo != "" {
		opts = append(opts, gofresh.WithBuildInputs(pgo))
	}
	return gofresh.New(opts...)
}

func runStatus(w io.Writer, benchDir, label string, staleOnly, explain, jsonOut bool, patterns []string) error {
	pkgs, err := resolvePackages(patterns)
	if err != nil {
		return err
	}
	for _, p := range pkgs {
		if p.Module.Dir == "" {
			continue // not in a module (e.g. a stdlib pattern) — nothing to record
		}
		// A per-package failure (an unreadable PGO profile, a sibling that
		// does not compile) is reported as a row and does not abort status of
		// the rest of the tree.
		reportErr := func(err error) {
			if jsonOut {
				_ = writeJSONLine(w, statusJSONRow{Package: p.ImportPath, Error: err.Error()})
				return
			}
			fmt.Fprintf(w, "%-12s %s  (%v)\n", "error", p.ImportPath, err)
		}
		e, _, err := newEngineForPkg(p, os.Environ())
		if err != nil {
			reportErr(err)
			continue
		}
		if err := statusPackage(w, e, benchDir, label, staleOnly, explain, jsonOut, p); err != nil {
			reportErr(err)
		}
	}
	return nil
}

func statusPackage(w io.Writer, e *gofresh.Engine, benchDir, label string, staleOnly bool, explain, jsonOut bool, p pkgMeta) error {
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
		v, reason, fp, err := checkOne(st, e, p.ImportPath, pkgRel, p.Module.Dir, b, label)
		if err != nil {
			return err
		}
		if staleOnly && v == verdictValid {
			continue
		}
		if jsonOut {
			if err := writeJSONLine(w, statusJSONRow{Package: p.ImportPath, Benchmark: b, Label: label, Verdict: string(v), Reason: reason}); err != nil {
				return err
			}
			continue
		}
		name := b
		if label != "" {
			// The row names the recording it inventoried: the labeled variant
			// carries its label exactly as its filename does.
			name = b + "." + label
		}
		line := fmt.Sprintf("%-12s %s.%s", v, p.ImportPath, name)
		if reason != "" {
			line += "  (" + reason + ")"
		}
		fmt.Fprintln(w, line)
		// fp.MaximalClosure is non-empty iff the format check passed: shape
		// validation requires a pew-closure key, so the empty sentinel is
		// exactly the unrecorded/stale-format/error set, which has nothing
		// decodable to tabulate.
		if explain && v != verdictValid && v != verdictUnrecorded && fp.MaximalClosure != "" {
			explainRecordAgainstCurrent(w, e, p.Module.Dir, p.ImportPath, b, fp, os.Environ())
		}
	}
	return nil
}

// checkOne is the per-benchmark validity verdict shared by status, run --stale,
// and stat's working-tree staleness warning. The engine recomputes the current
// closure and guards (the SSA load is the dominant cost; an unrecorded benchmark
// needs no analysis, so the store is read first).
func checkOne(st *store.Store, e *gofresh.Engine, pkgPath, pkgRel, moduleDir, bench, label string) (verdict, string, gofresh.Fingerprint, error) {
	recs, err := st.Read(pkgRel, bench, label)
	switch {
	case errors.Is(err, store.ErrNotRecorded):
		return verdictUnrecorded, "", gofresh.Fingerprint{}, nil
	case err != nil:
		return "", "", gofresh.Fingerprint{}, err
	}
	if !store.IsRecordingShape(recs) {
		return verdictStale, "format", gofresh.Fingerprint{}, nil
	}
	fp, pure, ok := fingerprintFromConfig(recs[0].Config)
	if !ok {
		return verdictStale, "format", gofresh.Fingerprint{}, nil
	}
	v, err := e.Check(context.Background(), fp, gofresh.Subject{Package: pkgPath, Symbol: bench}, moduleDir)
	if err != nil {
		return "", "", gofresh.Fingerprint{}, err
	}
	v = applyPurity(v, pure)
	// The fingerprint the verdict was decided over rides back so an
	// explanation view describes the same recording — never a re-read that a
	// concurrent run could have replaced.
	return verdict(v.Status), v.Reason, fp, nil
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
// becomes valid — except an engine verdict carrying the //gofresh:external
// directive's reason: the in-code external declaration is not a blind spot the
// caller may vouch away (§7.5), exactly as the in-code //gofresh:pure channel is
// applied inside the engine itself (newEngineForPkg).
func applyPurity(v gofresh.Verdict, pure string) gofresh.Verdict {
	switch pure {
	case "false":
		if v.Status != gofresh.Stale {
			return gofresh.Verdict{Status: gofresh.Unverifiable, Reason: "impure"}
		}
	case "true":
		if v.Status == gofresh.Unverifiable && v.Reason != "external directive" {
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
