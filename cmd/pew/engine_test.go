package main

import (
	"strings"
	"testing"

	gofresh "github.com/greatliontech/gofresh"
)

// TestNewEngineHonorsDirectives pins the shared engine construction (§7.5: the
// //gofresh:pure directive is honored by every consumer — status, run, and stat all
// build their engine here): a directive-pure benchmark whose closure reaches file
// I/O checks valid, where an engine without the directive scan reports unverifiable.
func TestNewEngineHonorsDirectives(t *testing.T) {
	const pkg = "github.com/thegrumpylion/pew/internal/fixtures/purebench"
	const bench = "BenchmarkPureRead"
	var p pkgMeta
	p.ImportPath = pkg
	p.TestGoFiles = []string{"pure_test.go"}
	p.Module.Dir = "."
	e, err := newEngine([]pkgMeta{p})
	if err != nil {
		t.Fatalf("newEngine: %v", err)
	}
	subj := gofresh.Subject{Package: pkg, Symbol: bench}
	fp, err := e.Capture(subj, ".")
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	v, err := e.Check(fp, subj, ".", gofresh.Measurement)
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if v.Status != gofresh.Valid {
		t.Errorf("directive-pure subject = {%s %q}, want valid", v.Status, v.Reason)
	}

	// Control: a plain engine (no directive scan) reports the same subject
	// unverifiable, proving the verdict above came from the directive.
	plain, err := gofresh.New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	fp2, err := plain.Capture(subj, ".")
	if err != nil {
		t.Fatalf("Capture plain: %v", err)
	}
	v2, err := plain.Check(fp2, subj, ".", gofresh.Measurement)
	if err != nil {
		t.Fatalf("Check plain: %v", err)
	}
	if v2.Status != gofresh.Unverifiable || !strings.Contains(v2.Reason, "os.ReadFile") {
		t.Errorf("plain engine = {%s %q}, want unverifiable (reaches os.ReadFile)", v2.Status, v2.Reason)
	}
}
