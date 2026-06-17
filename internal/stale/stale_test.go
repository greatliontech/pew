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

func rt(hash string) RuntimeState { return RuntimeState{Digest: hash, OK: true} }

func recordFor(p provenance.Provenance, hash string) []benchfmt.Config {
	return cfg(
		"commit", p.Commit, "toolchain", p.Toolchain, "machine", p.Machine,
		"buildconfig", p.BuildConfig, "pew-closure", hash, "pew-runtime", "rt1",
		"pew-runtime-inputs", "manifest1", "dirty", "false",
	)
}

func TestCheckValid(t *testing.T) {
	p := prov()
	if v, reason := Check(p, cl("cl1"), rt("rt1"), recordFor(p, "cl1")); v != Valid || reason != "" {
		t.Errorf("got %v/%q, want valid", v, reason)
	}
}

func TestCheckUnrecorded(t *testing.T) {
	if v, _ := Check(prov(), cl("cl1"), rt("rt1"), nil); v != Unrecorded {
		t.Errorf("got %v, want unrecorded", v)
	}
}

// TestCheckUnverifiable enforces INV-2/§7.3: after the guards pass, a HEAD
// closure reaching an unhashable non-file external dependence is Unverifiable,
// not valid — unless suppressed by --assume-pure (pure:true); and --impure
// (pure:false) is Unverifiable.
func TestCheckUnverifiable(t *testing.T) {
	p := prov()
	headB := closure.Closure{Hash: "cl1", Unverifiable: true, Reason: "reaches net.Dial (network I/O)"}

	// Class B reachable, no purity directive ⇒ unverifiable (absence of proof ≠ valid).
	if v, reason := Check(p, headB, rt("rt1"), recordFor(p, "cl1")); v != Unverifiable || reason != "reaches net.Dial (network I/O)" {
		t.Errorf("class-B: got %v/%q, want unverifiable/<reason>", v, reason)
	}
	// --assume-pure (pure:true) suppresses Class-B ⇒ falls through to the guards (valid here).
	pureRec := append(recordFor(p, "cl1"), benchfmt.Config{Key: "pure", Value: []byte("true")})
	if v, _ := Check(p, headB, rt("rt1"), pureRec); v != Valid {
		t.Errorf("--assume-pure: got %v, want valid (Class-B suppressed)", v)
	}
	// --impure (pure:false) ⇒ unverifiable when the guards match, even with no Class-B.
	impureRec := append(recordFor(p, "cl1"), benchfmt.Config{Key: "pure", Value: []byte("false")})
	if v, reason := Check(p, cl("cl1"), rt("rt1"), impureRec); v != Unverifiable || reason != "impure" {
		t.Errorf("--impure: got %v/%q, want unverifiable/impure", v, reason)
	}
}

func TestCheckRuntimeCoveredFileIOValid(t *testing.T) {
	p := prov()
	head := closure.Closure{Hash: "cl1", Unverifiable: true, Reason: "reaches os.ReadFile (file I/O)", RuntimeFileIOOnly: true}
	if v, reason := Check(p, head, rt("rt1"), recordFor(p, "cl1")); v != Valid || reason != "" {
		t.Errorf("runtime-covered file I/O: got %v/%q, want valid", v, reason)
	}
}

func TestCheckFileIORequiresClosureRuntimeOnlyProof(t *testing.T) {
	p := prov()
	head := closure.Closure{Hash: "cl1", Unverifiable: true, Reason: "reaches os.ReadFile (file I/O)"}
	if v, reason := Check(p, head, rt("rt1"), recordFor(p, "cl1")); v != Unverifiable || reason != head.Reason {
		t.Errorf("file I/O without runtime-only proof: got %v/%q, want unverifiable/%q", v, reason, head.Reason)
	}
}

func TestCheckGuardFailurePrecedesUnverifiable(t *testing.T) {
	p := prov()
	rec := recordFor(p, "cl1")
	if v, reason := Check(p, closure.Closure{Hash: "cl2", Unverifiable: true}, rt("rt1"), rec); v != Stale || reason != "pew-closure" {
		t.Errorf("class-B with stale closure: got %v/%q, want stale/pew-closure", v, reason)
	}
	impureRec := append(recordFor(p, "cl1"), benchfmt.Config{Key: "pure", Value: []byte("false")})
	if v, reason := Check(p, cl("cl2"), rt("rt1"), impureRec); v != Stale || reason != "pew-closure" {
		t.Errorf("impure with stale closure: got %v/%q, want stale/pew-closure", v, reason)
	}
}

func TestCheckStalePerGuard(t *testing.T) {
	p := prov()
	base := recordFor(p, "cl1")
	for _, tc := range []struct {
		mutP   provenance.Provenance
		mutCl  string
		mutRT  string
		reason string
	}{
		{p, "cl2", "rt1", "pew-closure"},
		{p, "cl1", "rt2", "pew-runtime"},
		{provenance.Provenance{Commit: "c1", Toolchain: "go1.27", Machine: "m1", BuildConfig: "b1"}, "cl1", "rt1", "toolchain"},
		{provenance.Provenance{Commit: "c1", Toolchain: "go1.26.4", Machine: "m2", BuildConfig: "b1"}, "cl1", "rt1", "machine"},
		{provenance.Provenance{Commit: "c1", Toolchain: "go1.26.4", Machine: "m1", BuildConfig: "b2"}, "cl1", "rt1", "buildconfig"},
	} {
		if v, reason := Check(tc.mutP, cl(tc.mutCl), rt(tc.mutRT), base); v != Stale || reason != tc.reason {
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
	if v, _ := Check(p, cl("cl1"), rt("rt1"), rec); v != Valid {
		t.Errorf("INV-6 violated: differing commit changed verdict to %v", v)
	}
}

func TestCheckMissingGuardIsStale(t *testing.T) {
	p := prov()
	rec := cfg("toolchain", p.Toolchain, "machine", p.Machine, "buildconfig", p.BuildConfig) // no pew-closure
	if v, reason := Check(p, cl("cl1"), rt("rt1"), rec); v != Stale || reason != "pew-closure" {
		t.Errorf("missing closure: got %v/%q, want stale/pew-closure", v, reason)
	}

	rec = cfg(
		"toolchain", p.Toolchain, "machine", p.Machine, "buildconfig", p.BuildConfig,
		"pew-closure", "cl1",
	) // no runtime metadata
	if v, reason := Check(p, cl("cl1"), rt("rt1"), rec); v != Stale || reason != "pew-runtime" {
		t.Errorf("missing runtime: got %v/%q, want stale/pew-runtime", v, reason)
	}
}

func TestCheckRuntimeUnverifiable(t *testing.T) {
	p := prov()
	runtime := RuntimeState{Digest: "rt1", OK: true, Unverifiable: true, Reason: "external directory input: /tmp"}
	if v, reason := Check(p, cl("cl1"), runtime, recordFor(p, "cl1")); v != Unverifiable || reason != runtime.Reason {
		t.Errorf("got %v/%q, want runtime unverifiable", v, reason)
	}
}
