package main

import (
	"testing"

	gofresh "github.com/greatliontech/gofresh"
)

// TestNewEngineHonorsDirectives pins the shared engine construction (§7.5: the
// //gofresh:pure directive is honored by every consumer — status, run, and stat all
// build their engine here): a directive-pure benchmark whose closure reaches file
// I/O checks valid, where an engine without the directive scan reports unverifiable.
func TestNewEngineHonorsDirectives(t *testing.T) {
	const pkg = "github.com/greatliontech/pew/internal/fixtures/purebench"
	const bench = "BenchmarkPureRead"
	e, err := newEngine(".")
	if err != nil {
		t.Fatalf("newEngine: %v", err)
	}
	subj := gofresh.Subject{Package: pkg, Symbol: bench}
	fp, err := e.CaptureFor(t.Context(), subj, ".", gofresh.Measurement)
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	v, err := e.Check(t.Context(), fp, subj, ".")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if v.Status != gofresh.Valid {
		t.Errorf("directive-pure subject = {%s %q}, want valid", v.Status, v.Reason)
	}
	if fp.PurityAssertion != "source directive" {
		t.Fatalf("purity attribution = %q, want source directive", fp.PurityAssertion)
	}
}
