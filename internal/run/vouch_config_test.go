package run

import "testing"

// The evidence lines gate independently: a benchmark whose only
// impurity was a vouched culprit still records its acceptance, the
// attestation-borne discharge sets record exactly when load-bearing,
// and nothing emits an empty line (spec §5).
func TestGofreshEvidenceConfigsGateIndependently(t *testing.T) {
	keys := func(purity, vouches, single, pkgProc string) []string {
		var out []string
		for _, cfg := range GofreshEvidenceConfigs(purity, vouches, single, pkgProc) {
			out = append(out, cfg.Key+"="+string(cfg.Value))
		}
		return out
	}
	if got := keys("", "", "", ""); len(got) != 0 {
		t.Fatalf("empty evidence emitted lines: %v", got)
	}
	if got := keys("caller assertion", "", "", ""); len(got) != 1 || got[0] != "pew-purity=caller assertion" {
		t.Fatalf("purity-only = %v", got)
	}
	if got := keys("", "a.example/dep.Var", "", ""); len(got) != 1 || got[0] != "pew-vouches=a.example/dep.Var" {
		t.Fatalf("vouches-only = %v", got)
	}
	if got := keys("", "", "a.example/pool.Var", ""); len(got) != 1 || got[0] != "pew-single-subject-discharges=a.example/pool.Var" {
		t.Fatalf("single-subject-only = %v", got)
	}
	if got := keys("", "", "", "a.example/proc.Var"); len(got) != 1 || got[0] != "pew-package-process-discharges=a.example/proc.Var" {
		t.Fatalf("package-process-only = %v", got)
	}
	if got := keys("caller assertion", "a.example/dep.Var", "a.example/pool.Var", "a.example/proc.Var"); len(got) != 4 || got[1] != "pew-vouches=a.example/dep.Var" || got[3] != "pew-package-process-discharges=a.example/proc.Var" {
		t.Fatalf("all = %v", got)
	}
}
