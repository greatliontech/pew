package run

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestExecutionRetainsAdmittedEnvironment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell environment fixture")
	}
	t.Setenv("PEW_EXECUTION_SNAPSHOT", "admitted")
	env := testEnvironment(t, nil)
	t.Setenv("PEW_EXECUTION_SNAPSHOT", "later")
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	out, err := ExecuteBinaryContext(ctx, t.TempDir(), "", env, "sh", []string{"-c", "printf '%s' \"$PEW_EXECUTION_SNAPSHOT\""})
	if err != nil || string(out) != "admitted" {
		t.Fatalf("execution environment = %q, error %v", out, err)
	}
}

func TestMeasurementRefusesUndrainedOutput(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell pipe-holder fixture")
	}
	dir := t.TempDir()
	release, done := filepath.Join(dir, "release"), filepath.Join(dir, "done")
	t.Setenv("PEW_MEASUREMENT_RELEASE", release)
	t.Setenv("PEW_MEASUREMENT_DONE", done)
	t.Cleanup(func() {
		if err := os.WriteFile(release, nil, 0600); err != nil {
			t.Error(err)
			return
		}
		deadline := time.After(5 * time.Second)
		tick := time.NewTicker(10 * time.Millisecond)
		defer tick.Stop()
		for {
			if _, err := os.Stat(done); err == nil {
				return
			}
			select {
			case <-deadline:
				t.Error("pipe holder did not acknowledge cleanup")
				return
			case <-tick.C:
			}
		}
	})
	// The retained prefix is a complete sample. It cannot prove that another
	// whole sub-benchmark was not lost when the output pipe was cut.
	script := "printf 'BenchmarkProbe/first-8 1 5 ns/op\\nPASS\\n'\n(while [ ! -e \"$PEW_MEASUREMENT_RELEASE\" ]; do sleep 0.01; done; : > \"$PEW_MEASUREMENT_DONE\") &\nexit 0\n"
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	out, err := ExecuteBinaryContext(ctx, dir, "", testEnvironment(t, nil), "sh", []string{"-c", script})
	if len(out) != 0 || !errors.Is(err, exec.ErrWaitDelay) || !strings.Contains(err.Error(), "undrained measurement output") {
		t.Fatalf("pipe-held measurement: output %q, error %v; want explicit refusal", out, err)
	}
}
