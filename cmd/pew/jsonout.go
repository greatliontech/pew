package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math"

	"github.com/greatliontech/pew/internal/compare"
	"golang.org/x/perf/benchmath"
)

// The -json output shapes (spec §12) are public surface: one JSON object per
// line, stable field names, deliberately excluding internal values (raw guard
// digests, closure hashes) — those belong to --explain's human view.

// statusJSONRow is one `pew status -json` line.
type statusJSONRow struct {
	Package   string `json:"package"`
	Benchmark string `json:"benchmark,omitempty"`
	Label     string `json:"label,omitempty"`
	Verdict   string `json:"verdict,omitempty"`
	Reason    string `json:"reason,omitempty"`
	Error     string `json:"error,omitempty"`
}

func writeJSONLine(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "%s\n", b)
	return err
}

// statJSONSummary carries a side's center and confidence interval. Lo/Hi are
// null when the interval is non-finite — benchmath legitimately reports ±Inf
// bounds below its finite-CI sample minimum, JSON cannot carry Inf, and the
// cause already rides the row's warnings (spec §12).
type statJSONSummary struct {
	Center     float64  `json:"center"`
	Lo         *float64 `json:"lo"`
	Hi         *float64 `json:"hi"`
	Confidence float64  `json:"confidence"`
}

func finiteOrNull(v float64) *float64 {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return nil
	}
	return &v
}

func summaryJSON(s benchmath.Summary) statJSONSummary {
	return statJSONSummary{Center: s.Center, Lo: finiteOrNull(s.Lo), Hi: finiteOrNull(s.Hi), Confidence: s.Confidence}
}

// statJSONRow is one `pew stat -json` comparison line ("kind": "row"); notes
// emit as {"kind":"note","text":...} and an empty comparison as
// {"kind":"empty","reason":...}.
type statJSONRow struct {
	Kind       string          `json:"kind"`
	Config     string          `json:"config,omitempty"`
	Unit       string          `json:"unit"`
	Benchmark  string          `json:"benchmark"`
	Base       statJSONSummary `json:"base"`
	New        statJSONSummary `json:"new"`
	P          float64         `json:"p"`
	DeltaPct   *float64        `json:"deltaPct"` // null when the baseline center is 0
	Regression bool            `json:"regression"`
	Gated      bool            `json:"gated"`
	Warnings   []string        `json:"warnings,omitempty"`
}

type statJSONNote struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

type statJSONEmpty struct {
	Kind   string `json:"kind"`
	Reason string `json:"reason"`
}

// writeStatJSON renders a comparison as line-delimited JSON: every compared
// row, then every not-compared note; an empty comparison emits one
// {"kind":"empty"} object carrying the same cause the text view names.
func writeStatJSON(w io.Writer, res *compare.Result, emptyReason func() string) error {
	if len(res.Tables) == 0 && len(res.Notes) == 0 {
		return writeJSONLine(w, statJSONEmpty{Kind: "empty", Reason: emptyReason()})
	}
	for _, table := range res.Tables {
		for _, row := range table.Rows {
			out := statJSONRow{
				Kind:       "row",
				Config:     table.Config,
				Unit:       table.Unit,
				Benchmark:  row.Name,
				Base:       summaryJSON(row.Base),
				New:        summaryJSON(row.New),
				P:          row.Cmp.P,
				Regression: row.Regression,
				Gated:      row.Gated,
				Warnings:   row.Warnings,
			}
			if !math.IsNaN(row.DeltaPct) {
				d := row.DeltaPct
				out.DeltaPct = &d
			}
			if err := writeJSONLine(w, out); err != nil {
				return err
			}
		}
	}
	for _, note := range res.Notes {
		if err := writeJSONLine(w, statJSONNote{Kind: "note", Text: note}); err != nil {
			return err
		}
	}
	return nil
}
