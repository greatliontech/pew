package compare

import (
	"strings"
	"testing"

	"github.com/greatliontech/pew/internal/run"
)

func TestTransientAuditWithoutNativeFingerprint(t *testing.T) {
	base := auditSet("X/child-8", map[string]string{run.KeyRunConditions.Name: quietConds}, map[string][]float64{"sec/op": rep(1, 8)})
	newer := auditSet("X/child-8", map[string]string{run.KeyRunConditions.Name: strings.Replace(quietConds, "turbo=off", "turbo=on", 1)}, map[string][]float64{"sec/op": rep(1, 8)})
	if base[0].GetConfig(run.KeyFingerprint.Name) != "" || newer[0].GetConfig(run.KeyFingerprint.Name) != "" {
		t.Fatal("fixture is not a transient stream")
	}
	r := Compare(base, newer, DefaultOptions())
	if r.ComparedRows() != 1 || r.GatedComparisons() != 1 || len(r.Notes) != 1 || !strings.Contains(r.Notes[0], "run conditions differ") || !strings.Contains(r.Notes[0], "turbo=off") || !strings.Contains(r.Notes[0], "turbo=on") {
		t.Fatalf("transient audit lost: %+v", r)
	}
}
