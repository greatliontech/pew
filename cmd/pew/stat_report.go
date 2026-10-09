package main

import (
	"fmt"
	"io"
	"sort"

	"github.com/greatliontech/pew/internal/compare"
	"golang.org/x/perf/benchfmt"
)

func blockedStatUnits(m *statModule, key statKey, bl baseline, resolved statSubjects, opts compare.Options, causes []compare.Cause) *compare.Result {
	// Readable children keep full identities through recording-level refusals.
	var sides [][]*benchfmt.Result
	for _, side := range []string{bl.baseRef, bl.newRef} {
		cached := m.sides[statSideKey{side, key.pkgRel, key.bench, key.label}]
		if cached.admitted != nil && cached.admitted.class != "format" {
			sides = append(sides, cached.recs)
		} else {
			sides = append(sides, nil)
		}
	}
	if len(sides[0]) > 0 || len(sides[1]) > 0 {
		opts.Blockers = causes
		subjects := map[*benchfmt.Result]compare.Subject{}
		resolved.bind(subjects, sides[0], sides[1])
		opts.SubjectOf = func(r *benchfmt.Result) compare.Subject { return subjects[r] }
		r := compare.Compare(sides[0], sides[1], opts)
		for i := range r.Dispositions {
			d := &r.Dispositions[i]
			// An unreadable side is unknown, not absent.
			kept := d.Causes[:0]
			for _, c := range d.Causes {
				unknown := false
				if c.Code == "missing-side" || c.Code == "missing-unit" {
					for _, b := range causes {
						if b.Side == c.Side && (b.Code == "format" || b.Code == "inventory") {
							unknown = true
						}
					}
				}
				if !unknown {
					kept = append(kept, c)
				}
			}
			d.Causes = kept
			d.OrderCauses()
		}
		// A format-invalid side is unavailable, not a missing admitted arm.
		// The retained structured dispositions already name that refusal.
		if len(sides[0]) == 0 || len(sides[1]) == 0 {
			r.Notes = nil
		}
		return r
	}
	var units []string
	for unit, requested := range opts.GateUnits {
		if requested {
			units = append(units, unit)
		}
	}
	sort.Strings(units)
	var out []compare.Disposition
	for _, unit := range units {
		d := compare.Disposition{Unit: unit, Requested: true, Causes: append([]compare.Cause{}, causes...)}
		resolved.describe(&d)
		d.OrderCauses()
		out = append(out, d)
	}
	return &compare.Result{Dispositions: out}
}

func writeStatReportText(w io.Writer, res *compare.Result, c compare.Coverage) error {
	if len(res.Tables) == 0 && len(res.Notes) == 0 {
		if _, err := fmt.Fprintln(w, "no recorded benchmarks to compare:", res.EmptyReason(c.Policies)); err != nil {
			return err
		}
	} else if err := res.WriteText(w); err != nil {
		return err
	}
	for _, d := range res.Dispositions {
		if _, err := fmt.Fprintln(w, "note:", d.Text()); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(w, c.Text())
	return err
}

func writeStatReportJSON(w io.Writer, res *compare.Result, c compare.Coverage) error {
	if len(res.Dispositions) == 0 && len(res.Tables) == 0 && len(res.Notes) == 0 {
		return writeJSONLine(w, struct {
			Kind    string           `json:"kind"`
			Reason  string           `json:"reason"`
			Details compare.Coverage `json:"details"`
		}{"empty", res.EmptyReason(c.Policies), c})
	}
	if err := writeStatJSON(w, res, func() string { return res.EmptyReason(c.Policies) }); err != nil {
		return err
	}
	for _, d := range res.Dispositions {
		if err := writeJSONLine(w, struct {
			Kind    string              `json:"kind"`
			Text    string              `json:"text"`
			Code    string              `json:"code"`
			Details compare.Disposition `json:"details"`
		}{"note", d.Text(), "disposition", d}); err != nil {
			return err
		}
	}
	return writeJSONLine(w, struct {
		Kind    string           `json:"kind"`
		Text    string           `json:"text"`
		Code    string           `json:"code"`
		Details compare.Coverage `json:"details"`
	}{"note", c.Text(), "coverage", c})
}
