package compare

import (
	"strings"
	"testing"

	"github.com/greatliontech/pew/internal/recordingtest"
	"github.com/greatliontech/pew/internal/run"
)

func TestComparisonDerivesNativeGuardsAndAudit(t *testing.T) {
	values := []float64{1, 2, 3, 4, 5, 6}
	base := recordingtest.Results("X", values)
	for _, key := range run.GuardRecordingKeys {
		newer := recordingtest.Results("X", values, recordingtest.Set(key, "different"))
		result := Compare(base, newer, DefaultOptions())
		if result.ComparedRows() != 0 || result.GuardMismatch != 1 || !strings.Contains(strings.Join(result.Notes, "\n"), key.Display+" mismatch") {
			t.Fatalf("%s: %+v", key.Name, result)
		}
	}
	for _, key := range run.AuditRecordingKeys {
		if key.Name == run.KeyRunConditions.Name {
			continue
		}
		newer := recordingtest.Results("X", values, recordingtest.Set(key, "different"))
		result := Compare(base, newer, DefaultOptions())
		if result.ComparedRows() != 1 || !strings.Contains(strings.Join(result.Notes, "\n"), key.Display+" differ") {
			t.Fatalf("%s: %+v", key.Name, result)
		}
	}
	newer := recordingtest.Results("X", values, recordingtest.Set(run.KeyClosure, "different"), recordingtest.Set(run.KeyRuntimeInputs, strings.Repeat("large", 20000)))
	result := Compare(base, newer, DefaultOptions())
	if result.ComparedRows() != 1 || result.Tables[0].Config != "" {
		t.Fatalf("native payload fragmented comparison: %+v", result)
	}
	// Payload presence never falls back to a parallel guard when malformed.
	for i := range newer[0].Config {
		if newer[0].Config[i].Key == run.KeyFingerprint.Name {
			newer[0].Config[i].Value = []byte("corrupt")
		}
	}
	newer[0].Config = append(newer[0].Config, run.KeyToolchain.Config("go-test"))
	result = Compare(base, newer, DefaultOptions())
	if result.ComparedRows() != 0 || result.GuardMismatch != 1 {
		t.Fatalf("malformed native payload borrowed a parallel guard: %+v", result)
	}
}
