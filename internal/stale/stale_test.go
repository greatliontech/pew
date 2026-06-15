package stale

import (
	"testing"

	"github.com/thegrumpylion/pew/internal/closure"
	"github.com/thegrumpylion/pew/internal/provenance"
	"golang.org/x/perf/benchfmt"
)

func cfg(pairs ...string) []benchfmt.Config {
	var c []benchfmt.Config
	for i := 0; i+1 < len(pairs); i += 2 {
		c = append(c, benchfmt.Config{Key: pairs[i], Value: []byte(pairs[i+1])})
	}
	return c
}

// cl is a verifiable HEAD closure with the given hash.
func cl(hash string) closure.Closure { return closure.Closure{Hash: hash} }

func prov() provenance.Provenance {
	return provenance.Provenance{Commit: "c1", Toolchain: "go1.26.4", Machine: "m1", BuildConfig: "b1"}
}

func recordFor(p provenance.Provenance, hash string) []benchfmt.Config {
	return cfg(
		"commit", p.Commit, "toolchain", p.Toolchain, "machine", p.Machine,
		"buildconfig", p.BuildConfig, "pew-closure", hash, "dirty", "false",
	)
}

func TestCheckValid(t *testing.T) {
	p := prov()
	if v, reason := Check(p, cl("cl1"), recordFor(p, "cl1")); v != Valid || reason != "" {
		t.Errorf("got %v/%q, want valid", v, reason)
	}
}

func TestCheckUnrecorded(t *testing.T) {
	if v, _ := Check(prov(), cl("cl1"), nil); v != Unrecorded {
		t.Errorf("got %v, want unrecorded", v)
	}
}

// TestCheckUnverifiable enforces INV-2/§7.3: after the guards pass, a HEAD
// closure reaching an unhashable external dependence is Unverifiable, not valid —
// unless suppressed by --assume-pure (pure:true); and --impure (pure:false) is
// Unverifiable.
func TestCheckUnverifiable(t *testing.T) {
	p := prov()
	headB := closure.Closure{Hash: "cl1", Unverifiable: true, Reason: "reaches os.Open (file I/O)"}

	// Class B reachable, no purity directive ⇒ unverifiable (absence of proof ≠ valid).
	if v, reason := Check(p, headB, recordFor(p, "cl1")); v != Unverifiable || reason != "reaches os.Open (file I/O)" {
		t.Errorf("class-B: got %v/%q, want unverifiable/<reason>", v, reason)
	}
	// --assume-pure (pure:true) suppresses Class-B ⇒ falls through to the guards (valid here).
	pureRec := append(recordFor(p, "cl1"), benchfmt.Config{Key: "pure", Value: []byte("true")})
	if v, _ := Check(p, headB, pureRec); v != Valid {
		t.Errorf("--assume-pure: got %v, want valid (Class-B suppressed)", v)
	}
	// --impure (pure:false) ⇒ unverifiable when the guards match, even with no Class-B.
	impureRec := append(recordFor(p, "cl1"), benchfmt.Config{Key: "pure", Value: []byte("false")})
	if v, reason := Check(p, cl("cl1"), impureRec); v != Unverifiable || reason != "impure" {
		t.Errorf("--impure: got %v/%q, want unverifiable/impure", v, reason)
	}
}

func TestCheckGuardFailurePrecedesUnverifiable(t *testing.T) {
	p := prov()
	rec := recordFor(p, "cl1")
	if v, reason := Check(p, closure.Closure{Hash: "cl2", Unverifiable: true}, rec); v != Stale || reason != "pew-closure" {
		t.Errorf("class-B with stale closure: got %v/%q, want stale/pew-closure", v, reason)
	}
	impureRec := append(recordFor(p, "cl1"), benchfmt.Config{Key: "pure", Value: []byte("false")})
	if v, reason := Check(p, cl("cl2"), impureRec); v != Stale || reason != "pew-closure" {
		t.Errorf("impure with stale closure: got %v/%q, want stale/pew-closure", v, reason)
	}
}

func TestCheckStalePerGuard(t *testing.T) {
	p := prov()
	base := recordFor(p, "cl1")
	for _, tc := range []struct {
		mutP   provenance.Provenance
		mutCl  string
		reason string
	}{
		{p, "cl2", "pew-closure"},
		{provenance.Provenance{Commit: "c1", Toolchain: "go1.27", Machine: "m1", BuildConfig: "b1"}, "cl1", "toolchain"},
		{provenance.Provenance{Commit: "c1", Toolchain: "go1.26.4", Machine: "m2", BuildConfig: "b1"}, "cl1", "machine"},
		{provenance.Provenance{Commit: "c1", Toolchain: "go1.26.4", Machine: "m1", BuildConfig: "b2"}, "cl1", "buildconfig"},
	} {
		if v, reason := Check(tc.mutP, cl(tc.mutCl), base); v != Stale || reason != tc.reason {
			t.Errorf("guard %s: got %v/%q, want stale/%s", tc.reason, v, reason, tc.reason)
		}
	}
}

// TestCheckIgnoresCommit enforces INV-6: validity is commit-sha-independent.
func TestCheckIgnoresCommit(t *testing.T) {
	p := prov()
	rec := recordFor(p, "cl1")
	for i := range rec {
		if rec[i].Key == "commit" {
			rec[i].Value = []byte("a-totally-different-commit")
		}
	}
	if v, _ := Check(p, cl("cl1"), rec); v != Valid {
		t.Errorf("INV-6 violated: differing commit changed verdict to %v", v)
	}
}

func TestCheckMissingGuardIsStale(t *testing.T) {
	p := prov()
	rec := cfg("toolchain", p.Toolchain, "machine", p.Machine, "buildconfig", p.BuildConfig) // no pew-closure
	if v, reason := Check(p, cl("cl1"), rec); v != Stale || reason != "pew-closure" {
		t.Errorf("missing closure: got %v/%q, want stale/pew-closure", v, reason)
	}
}

func TestParseBenchList(t *testing.T) {
	got := parseBenchList([]byte("BenchmarkA\nBenchmarkB\nok  \tx\t0.001s\n"))
	if len(got) != 2 || got[0] != "BenchmarkA" || got[1] != "BenchmarkB" {
		t.Errorf("got %v, want [BenchmarkA BenchmarkB]", got)
	}
	if g := parseBenchList([]byte("?   \tx\t[no test files]\n")); len(g) != 0 {
		t.Errorf("expected no benchmarks, got %v", g)
	}
}

func TestListBenchmarks(t *testing.T) {
	names, err := ListBenchmarks("github.com/thegrumpylion/pew/internal/closure")
	if err != nil {
		t.Fatalf("ListBenchmarks: %v", err)
	}
	found := false
	for _, n := range names {
		if n == "BenchmarkHashFiles" {
			found = true
		}
	}
	if !found {
		t.Errorf("BenchmarkHashFiles not listed; got %v", names)
	}
}
