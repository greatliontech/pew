package main

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"path/filepath"

	"github.com/greatliontech/pew/internal/run"
)

// A context already cancelled when a verb reaches its package listing
// ends the verb with its interruption report and exit 130, never the
// raw `go list: context canceled` — the listing honors the verb's
// context now, so the cancellation must read as REQ-pew-interruption's
// "keeps what was persisted and says so", not a bare tool error. One
// anchor per verb; stat's is TestStatInterruptedEndsWithAReport.
func TestVerbsReportACancelledListingAsInterruption(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/i\n\ngo 1.24\n")
	withWorkingDir(t, dir)
	cancelled := func() context.Context {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		return ctx
	}
	assertInterrupted := func(name string, err error) {
		t.Helper()
		var stopped *interruptedError
		if !errors.As(err, &stopped) {
			t.Fatalf("%s under a cancelled context = %v; want the interruption report", name, err)
		}
		if exitCode(err) != 130 {
			t.Fatalf("%s exit code %d; want 130", name, exitCode(err))
		}
	}
	var w, ew bytes.Buffer
	assertInterrupted("run", runRun(cancelled(), &w, &ew, runConfig{benchDir: filepath.Join(dir, "b"), opts: run.Options{Count: 1}}, []string{"./..."}))
	assertInterrupted("status", runStatus(cancelled(), &w, filepath.Join(dir, "b"), "", false, false, false, []string{"./..."}))
	assertInterrupted("gc", runGC(cancelled(), &w, filepath.Join(dir, "b")))
	assertInterrupted("ab", runAB(cancelled(), &w, &ew, abConfig{bench: ".", count: 1, benchtime: "1x", ref: "HEAD"}, []string{"./..."}))
}
