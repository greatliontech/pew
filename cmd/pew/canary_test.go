package main

import (
	"os"
	"path/filepath"
	"testing"

	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/shapecorpus"
)

// TestLanguageShapeCanaries runs the fleet's shared shape corpus
// (gofresh/shapecorpus) through pew's measurement capture: each
// entry's benchmark closure captures and checks without an
// analysis-class failure. Runs under the CI matrix's next-rc leg like
// every test, so a new Go release's shape breakage fails HERE as a
// named canary instead of a stale-store field session.
func TestLanguageShapeCanaries(t *testing.T) {
	for _, entry := range shapecorpus.Entries() {
		t.Run(entry.Name, func(t *testing.T) {
			dir := t.TempDir()
			for file, content := range entry.BenchFiles() {
				if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			e, _, err := newEngineAt(dir, dir, false, os.Environ())
			if err != nil {
				t.Errorf("canary engine: %v", err)
				return
			}
			subj := gofresh.Subject{Package: "example.com/shape", Symbol: "BenchmarkSubject"}
			fp, err := e.CaptureFor(t.Context(), subj, dir, gofresh.Measurement)
			if err != nil {
				t.Errorf("canary capture: %v", err)
				return
			}
			if _, err := e.Check(t.Context(), fp, subj, dir); err != nil {
				t.Errorf("canary check: %v", err)
			}
		})
	}
}
