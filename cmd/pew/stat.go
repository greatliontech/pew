package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/thegrumpylion/pew/internal/closure"
	"github.com/thegrumpylion/pew/internal/compare"
	"github.com/thegrumpylion/pew/internal/gitblob"
	"github.com/thegrumpylion/pew/internal/provenance"
	"github.com/thegrumpylion/pew/internal/stale"
	"github.com/thegrumpylion/pew/internal/store"
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
with 'pew run' first). It scans ./... for benchmarks to compare.`,
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

func runStat(w, errw io.Writer, sc statConfig, refs []string) error {
	bl, err := baselineFor(refs)
	if err != nil {
		return err
	}
	pkgs, err := resolvePackages([]string{"./..."})
	if err != nil {
		return err
	}

	repos := map[string]*gitblob.Repo{} // cached per module dir
	var baseAll, newAll []*benchfmt.Result

	// When the new side is the working-tree recording (auto/pinned), a recording
	// that is stale for HEAD does not reflect current code, so its "new" numbers
	// are misleading — warn (don't block), pointing at `pew run`. The closure
	// hasher is only needed for that check, so it is built only then. In A/B mode
	// both sides are historical recordings and no staleness check applies.
	var hasher *closure.Hasher
	if bl.newRef == "" {
		if hasher, err = closure.New(); err != nil {
			return err
		}
	}

	for _, p := range pkgs {
		if p.Module.Dir == "" {
			continue // not in a module (e.g. a stdlib pattern) — nothing recorded
		}
		repo := repos[p.Module.Dir]
		if repo == nil {
			if repo, err = gitblob.Open(p.Module.Dir); err != nil {
				return err
			}
			repos[p.Module.Dir] = repo
		}
		dir := sc.benchDir
		switch {
		case dir == "":
			dir = filepath.Join(p.Module.Dir, "benchmarks")
		case !filepath.IsAbs(dir):
			// blob reads need an absolute, repo-relative path; resolve now.
			if dir, err = filepath.Abs(dir); err != nil {
				return err
			}
		}
		st := store.New(dir)
		pkgRel := strings.TrimPrefix(strings.TrimPrefix(p.ImportPath, p.Module.Path), "/")

		benches, err := selectedBenchmarks(p)
		if err != nil {
			// Consistent with status/run: a package whose benchmark declarations cannot
			// be read is reported and skipped, not fatal to the whole comparison.
			fmt.Fprintf(errw, "pew: warning: %s: %v\n", p.ImportPath, err)
			continue
		}
		if len(benches) == 0 {
			continue
		}

		// Provenance for the working-tree staleness check (the closure is per
		// benchmark, computed in the loop). Best-effort: a failure warns but never
		// blocks the comparison.
		var prov provenance.Provenance
		checkStale := false
		if bl.newRef == "" {
			if pv, e := provenance.Capture(p.Module.Dir); e != nil {
				fmt.Fprintf(errw, "pew: warning: %s: cannot check working-tree staleness: %v\n", p.ImportPath, e)
			} else {
				prov, checkStale = pv, true
			}
		}

		for _, b := range benches {
			baseRecs, baseOK, err := readSide(st, repo, bl.baseRef, pkgRel, b, sc.label)
			if err != nil {
				return err
			}
			newRecs, newOK, err := readSide(st, repo, bl.newRef, pkgRel, b, sc.label)
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
				fmt.Fprintf(errw, "pew: warning: baseline %s:%s is a dirty recording; skipping (spec §10)\n", bl.baseRef, b)
				continue
			}
			if newOK && bl.newRef != "" && isDirty(newRecs) {
				fmt.Fprintf(errw, "pew: warning: baseline %s:%s is a dirty recording; skipping (spec §10)\n", bl.newRef, b)
				continue
			}
			if checkStale && newOK {
				if cl, e := hasher.Compute(p.ImportPath, b); e != nil {
					fmt.Fprintf(errw, "pew: warning: %s.%s: cannot check working-tree staleness: %v\n", p.ImportPath, b, e)
				} else if v, reason := stale.Check(prov, cl, currentRuntimeState(newRecs[0].Config, p.Module.Dir), newRecs[0].Config); v != stale.Valid {
					msg := string(v)
					if reason != "" {
						msg += " (" + reason + ")"
					}
					fmt.Fprintf(errw, "pew: warning: working-tree recording %s.%s is %s; comparison may not reflect HEAD — re-run `pew run`\n", p.ImportPath, b, msg)
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
	return recs, true, nil
}
