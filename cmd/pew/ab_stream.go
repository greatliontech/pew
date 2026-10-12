package main

import (
	"fmt"
	"io"

	"github.com/greatliontech/pew/internal/run"
	"golang.org/x/perf/benchfmt"
)

// abStream is a side's parsed output admitted against its requested declarations
// and harness selection. It supplies rows for comparison and bytes for the
// derivation artifact; neither representation invents an execution receipt.
type abStream struct {
	pkg  string
	raw  []byte
	rows []*benchfmt.Result
}

func admitABStream(w io.Writer, raw []byte, pkg, pattern string, selected []string) (abStream, error) {
	rows, corrupt, dropped, err := run.Parse(raw)
	if err != nil {
		return abStream{}, err
	}
	for _, dc := range dropped {
		fmt.Fprintf(w, "pew: warning: dropping stream configuration key %q (value %q): not a toolchain benchmark key (spec §5)\n", dc.Key, dc.Value)
	}
	for _, cl := range corrupt {
		fmt.Fprintf(w, "pew: warning: corrupt benchmark output line %d: %q (%s)\n", cl.Line, cl.Text, cl.Cause)
	}
	if len(rows) == 0 || len(corrupt) != 0 {
		return abStream{}, fmt.Errorf("ab: incomplete or corrupt measurement stream")
	}
	if len(selected) == 0 {
		return abStream{}, fmt.Errorf("ab: stream lies outside requested declarations (none admitted)")
	}
	if err := selectionAcceptsRows(pattern, 1, rows, selected); err != nil {
		return abStream{}, err
	}
	if err := run.VerifyToolchainConfig(rows, run.ToolchainTruth{ImportPath: pkg}); err != nil {
		return abStream{}, err
	}
	audit := run.AuditStream(rows, corrupt, 1, selected)
	if audit.PackageCause != "" || len(audit.Refused) != 0 {
		return abStream{}, fmt.Errorf("ab: invalid measurement stream: %s %v", audit.PackageCause, audit.Refused)
	}
	return abStream{pkg: pkg, raw: raw, rows: rows}, nil
}
