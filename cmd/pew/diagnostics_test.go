package main

import (
	"bytes"
	"testing"

	"github.com/greatliontech/gofresh"
)

// TestEngineDiagnosticsDeliverDetailEvents pins the engines' progress
// consumer: a payload-bearing gofresh event reaches the operator's log
// as one line through gofresh's own sink — a multi-line detail folded
// onto it, a package-less event carrying no doubled space — and
// detail-free keep-alives stay silent (they name the reporter's
// stretch instead); the class of defect being a consumer that
// discards diagnostics by signature or renders them by hand.
func TestEngineDiagnosticsDeliverDetailEvents(t *testing.T) {
	var buf bytes.Buffer
	old := engineDiagnostics
	engineDiagnostics = gofresh.DiagnosticsTo(&buf)
	defer func() { engineDiagnostics = old }()
	emitEngineDiagnostic(gofresh.Progress{Phase: "observe", Package: "example.com/x"})
	if buf.Len() != 0 {
		t.Fatalf("keep-alive wrote %q, want silence", buf.String())
	}
	emitEngineDiagnostic(gofresh.Progress{Phase: "analysis-unavailable", Package: "example.com/x", Detail: "unsupported analysis shape: chan T"})
	if got, want := buf.String(), "gofresh: analysis-unavailable example.com/x — unsupported analysis shape: chan T\n"; got != want {
		t.Fatalf("diagnostic line = %q, want %q", got, want)
	}
	buf.Reset()
	emitEngineDiagnostic(gofresh.Progress{Phase: "toolchain", Detail: "release go1.99.0 unlisted\nwalk it"})
	if got, want := buf.String(), "gofresh: toolchain — release go1.99.0 unlisted; walk it\n"; got != want {
		t.Fatalf("package-less multi-line diagnostic = %q, want %q", got, want)
	}
}
