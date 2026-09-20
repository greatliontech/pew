package compare

import (
	"strings"
	"testing"

	"github.com/greatliontech/pew/internal/recordingtest"
	"github.com/greatliontech/pew/internal/run"
)

// TestEveryAuditKeyIsNoted: every registry row spec §5 marks audit
// produces a comparison note when the two sides differ in it and never
// splits the grouping — the run conditions through their categorical
// fields, every other row through the audit note carrying the row's
// display name (§10.1). A row marked audit in the table without its
// note here would be silent; this pin walks the table's mark.
func TestEveryAuditKeyIsNoted(t *testing.T) {
	audit := run.AuditRecordingKeys
	if len(audit) == 0 {
		t.Fatal("the registry marks no audit rows")
	}
	for _, k := range audit {
		t.Run(k.Name, func(t *testing.T) {
			baseVal, newVal := "side-a", "side-b"
			if k.Name == run.KeyRunConditions.Name {
				baseVal = recordingtest.QuietConditions
				newVal = "governor=powersave turbo=off load1=0.03 throttled=false battery=false"
			}
			res := Compare(
				auditSet("BenchmarkX-8", map[string]string{k.Name: baseVal}, map[string][]float64{"sec/op": seq(1000, 8)}),
				auditSet("BenchmarkX-8", map[string]string{k.Name: newVal}, map[string][]float64{"sec/op": seq(1100, 8)}),
				DefaultOptions(),
			)
			if len(res.Notes) != 1 || !strings.Contains(res.Notes[0], k.Display+" differ") {
				t.Fatalf("notes = %v, want one %q note", res.Notes, k.Display+" differ")
			}
			_ = secRow(t, res) // fails if the key fragmented grouping or blocked comparison
		})
	}
	// Two audit rows differing render their notes in the table's order —
	// the registry's, the one order the notes have: the vouches row is
	// the table's first generic audit row and the closure derivation its
	// last (a literal expectation, never the registry's own order).
	first, last := run.KeyVouches, run.KeyClosureStrategy
	res := Compare(
		auditSet("BenchmarkX-8", map[string]string{first.Name: "side-a", last.Name: "side-a"}, map[string][]float64{"sec/op": seq(1000, 8)}),
		auditSet("BenchmarkX-8", map[string]string{first.Name: "side-b", last.Name: "side-b"}, map[string][]float64{"sec/op": seq(1100, 8)}),
		DefaultOptions(),
	)
	if len(res.Notes) != 2 || !strings.Contains(res.Notes[0], first.Display) || !strings.Contains(res.Notes[1], last.Display) {
		t.Fatalf("notes = %v, want %q then %q", res.Notes, first.Display, last.Display)
	}
	// A non-audit row differing is silent and still compares: the mark
	// is what separates a note from silence.
	res = Compare(
		auditSet("BenchmarkX-8", map[string]string{run.KeyPurity.Name: "a"}, map[string][]float64{"sec/op": seq(1000, 8)}),
		auditSet("BenchmarkX-8", map[string]string{run.KeyPurity.Name: "b"}, map[string][]float64{"sec/op": seq(1100, 8)}),
		DefaultOptions(),
	)
	if len(res.Notes) != 0 {
		t.Fatalf("a non-audit row produced notes %v", res.Notes)
	}
	_ = secRow(t, res)
}

// TestGuardPrecedenceIsTableOrder: the guards are judged in spec §5's
// table order, so when two differ the note names the earlier row —
// toolchain before machine (a literal expectation: the table's order,
// never the derived set's).
func TestGuardPrecedenceIsTableOrder(t *testing.T) {
	res := Compare(
		auditSet("BenchmarkX-8", map[string]string{run.KeyMachine.Name: "m1", run.KeyToolchain.Name: "go1"}, map[string][]float64{"sec/op": seq(1000, 8)}),
		auditSet("BenchmarkX-8", map[string]string{run.KeyMachine.Name: "m2", run.KeyToolchain.Name: "go2"}, map[string][]float64{"sec/op": seq(1100, 8)}),
		DefaultOptions(),
	)
	if len(res.Notes) != 1 || !strings.Contains(res.Notes[0], "toolchain mismatch (base=go1 new=go2)") {
		t.Fatalf("notes = %v, want the toolchain mismatch named first", res.Notes)
	}
	if strings.Contains(res.Notes[0], "machine") {
		t.Fatalf("note names the later guard: %q", res.Notes[0])
	}
}
