package run

import "testing"

// The purity and vouch evidence lines gate independently: a benchmark
// whose only impurity was a vouched culprit still records its
// acceptance, and nothing emits an empty line (spec §5).
func TestGofreshEvidenceConfigsGateIndependently(t *testing.T) {
	keys := func(purity, vouches string) []string {
		var out []string
		for _, cfg := range GofreshEvidenceConfigs(purity, vouches) {
			out = append(out, cfg.Key+"="+string(cfg.Value))
		}
		return out
	}
	if got := keys("", ""); len(got) != 0 {
		t.Fatalf("empty evidence emitted lines: %v", got)
	}
	if got := keys("caller assertion", ""); len(got) != 1 || got[0] != "pew-purity=caller assertion" {
		t.Fatalf("purity-only = %v", got)
	}
	if got := keys("", "a.example/dep.Var"); len(got) != 1 || got[0] != "pew-vouches=a.example/dep.Var" {
		t.Fatalf("vouches-only = %v", got)
	}
	if got := keys("caller assertion", "a.example/dep.Var"); len(got) != 2 || got[1] != "pew-vouches=a.example/dep.Var" {
		t.Fatalf("both = %v", got)
	}
}
