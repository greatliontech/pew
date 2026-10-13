package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/pew/internal/profiles"
	"github.com/greatliontech/pew/internal/run"
	"github.com/greatliontech/pew/internal/store"
	"golang.org/x/perf/benchfmt"
)

type abProfileSide struct {
	prep *packagePreparation
	view *gofresh.Snapshot
}

func prepareABProfiles(ctx context.Context, packages [2]pkgMeta, selected [2][]string, envs environments) ([2]*abProfileSide, error) {
	var sides [2]*abProfileSide
	for i, p := range packages {
		names := selected[i]
		scratch, err := scratchPatterns(p)
		if err != nil {
			return sides, err
		}
		harnessArgs, err := prepareHarnessArgs(ctx, p.Module.Dir, envs.analysis)
		if err != nil {
			return sides, err
		}
		engine, pgo, err := newEngineForPkgProducer(ctx, p, envs.analysis, envs.runtime)
		if err != nil {
			return sides, err
		}
		subjects := make([]gofresh.Subject, 0, len(names))
		for _, name := range names {
			subjects = append(subjects, gofresh.Subject{Package: p.ImportPath, Symbol: name})
		}
		view, err := newViewFor(engine, ctx, subjects, p.Module.Dir, gofresh.Measurement)
		if err != nil {
			return sides, err
		}
		prep := &packagePreparation{pkg: p, pkgRel: packageRel(p), runBenches: names, scratch: scratch, engine: engine, pgoInput: pgo, harnessArgs: harnessArgs}
		sides[i] = &abProfileSide{prep: prep, view: view}
	}
	return sides, nil
}

func captureABProfiles(ctx context.Context, w, errw io.Writer, ac abConfig, preps []*abPreparation, envs environments, conditions run.Conditions) error {
	kinds, _ := profiles.Kinds(ac.profile)
	rc := runConfig{pin: ac.pin, strict: ac.strict, throttle: ac.throttle, diagnostic: ac.diagnostic}
	rc.opts.Bench = ac.bench
	gc := &gitStateCache{}
	var objects *store.Store
	if ac.out != "" {
		out, err := filepath.Abs(ac.out)
		if err != nil {
			return err
		}
		gc.exclude = []string{out, out + ".profiles"}
		objects = store.New(out + ".profiles")
	} else {
		fmt.Fprintln(errw, "pew: diagnostic objects are not retained without --out")
	}
	var failures []error
	unfulfilled := false
	for _, prep := range preps {
		names := map[string]bool{}
		for _, s := range prep.diagnostics {
			for _, name := range s.prep.runBenches {
				names[name] = true
			}
		}
		// The statistical rows define deterministic requested subject ordering.
		ordered := make([]string, 0, len(names))
		for name := range names {
			ordered = append(ordered, name)
		}
		sort.Strings(ordered)
		for _, name := range ordered {
			for _, kind := range kinds {
				report := profileComparison{Kind: "profile", Package: prep.pkg.ImportPath, Benchmark: name, Profile: kind}
				report.BasePackage, report.NewPackage = prep.reference.ImportPath, prep.pkg.ImportPath
				if report.BasePackage != report.NewPackage {
					report.Package = ""
				}
				var captures [2]*profiles.Capture
				var blobs [2][]byte
				for i, side := range prep.diagnostics {
					status := profileStatus{Kind: kind, Integrity: "unavailable", Freshness: "unavailable", Relation: "unverified", Outcome: "unavailable", Reason: "diagnostic not completed"}
					var rows []*benchfmt.Result
					for _, row := range prep.rows[i] {
						if run.BenchName(string(row.Name)) == name {
							rows = append(rows, row)
						}
					}
					selection, err := restrictBenchmarkPattern(ac.bench, []string{name})
					if err == nil && len(rows) == 0 {
						status.Reason = "no completed measurement on this side"
						unfulfilled = true
					}
					if err == nil && ctx.Err() != nil {
						err = ctx.Err()
					}
					if err == nil && len(rows) != 0 {
						err = captureDiagnostic(ctx, errw, rc, gc, side.prep, envs, conditions, side.view, name, kind, ac.profileBenchtime, selection, rows, nil, objects, func(gate context.Context, c profiles.Capture, blob []byte) error {
							// The ordinary comparison domain permits different source
							// coordinates. Bind it to this producing side through the
							// same native closing check; never rewrite native evidence.
							p := side.prep.pkg
							ordinary, err := ac.sideGuards(gate, p.Module.Dir, p.Dir, p.Name == "main", envs.analysis)
							if err != nil {
								return err
							}
							expected := prep.guardsA
							if i == 1 {
								expected = prep.guardsB
							}
							if ordinary != expected {
								return fmt.Errorf("ab: diagnostic comparison guards changed since its timed build")
							}
							if err := validateProfileSnapshot(gate, side.view, gofresh.Subject{Package: c.Package, Symbol: c.Benchmark}); err != nil {
								return err
							}
							facts, err := side.view.CurrentFacts(gate, gofresh.Subject{Package: c.Package, Symbol: c.Benchmark})
							if err != nil {
								return err
							}
							if c.Fingerprint.Guards != facts.Guards {
								return fmt.Errorf("ab: diagnostic guards differ from its timed build facts")
							}
							if len(c.Children) == 0 {
								return fmt.Errorf("ab: diagnostic has no children")
							}
							children := map[string]bool{}
							for _, r := range rows {
								children[string(r.Name)] = true
							}
							if len(children) != len(c.Children) {
								return fmt.Errorf("ab: diagnostic child selection differs")
							}
							for _, ch := range c.Children {
								if !children[ch.Name] {
									return fmt.Errorf("ab: diagnostic child selection differs")
								}
							}
							if ac.out != "" {
								if ac.artifact.ownership != nil {
									if err := ac.artifact.ownership.validate(); err != nil {
										return err
									}
								}
								// The shared confined store owns object mapping and immutable install.
								if _, err := objects.PutObject(gate, blob); err != nil {
									return err
								}
								sideName := []string{"A", "B"}[i]
								if err := ac.artifact.publishBlocks(gate, abBlock{pkg: c.Package, ref: ac.ref, side: sideName, profile: &c}); err != nil {
									return err
								}
							}
							metrics, err := profiles.Analyze(blob, kind, c.Sources)
							if err != nil {
								return err
							}
							empty := profiles.Empty(metrics)
							status = profileStatus{Package: c.Package, Benchmark: c.Benchmark, Kind: kind, Integrity: "verified", Freshness: "unverifiable", Relation: "unverified", Outcome: "identity-only", Reason: profiles.UnsupportedReason, Empty: &empty, Metrics: metrics}
							captures[i] = &c
							blobs[i] = blob
							return nil
						})
					}
					if err != nil {
						status.Error = err.Error()
						failures = append(failures, err)
					}
					if i == 0 {
						report.New = status
					} else {
						report.Base = status
					}
				}
				if captures[0] != nil && captures[1] != nil {
					var err error
					report.Differences, err = profiles.CompareWithGuards(*captures[1], *captures[0], blobs[1], blobs[0], prep.guardsB, prep.guardsA)
					if err != nil {
						report.Reason = err.Error()
						unfulfilled = true
					} else {
						report.Compared = true
					}
				}
				if err := writeProfileComparisons(w, []profileComparison{report}, ac.jsonOut); err != nil {
					return err
				}
				if ctx.Err() != nil {
					return errors.Join(interrupted("ab: interrupted; completed measurement pairs and diagnostic sides retained"), errors.Join(failures...))
				}
			}
		}
	}
	if len(failures) > 0 {
		return errors.Join(failures...)
	}
	if unfulfilled {
		return &profileUnfulfilledError{}
	}
	return nil
}
