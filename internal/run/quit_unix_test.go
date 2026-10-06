//go:build unix

package run

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

func TestMeasurementCancellationDoesNotSendQuit(t *testing.T) {
	if dir := os.Getenv("PEW_QUIT_PROBE"); dir != "" {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGQUIT)
		if err := os.WriteFile(filepath.Join(dir, "ready"), nil, 0600); err != nil {
			t.Fatal(err)
		}
		<-quit
		if err := os.WriteFile(filepath.Join(dir, "quit"), nil, 0600); err != nil {
			t.Fatal(err)
		}
		return
	}
	dir := t.TempDir()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	env := testEnvironment(t, append(os.Environ(), "PEW_QUIT_PROBE="+dir))
	go func() {
		_, err := ExecuteBinaryContext(ctx, dir, "", env, binary, []string{"-test.run=^TestMeasurementCancellationDoesNotSendQuit$"})
		done <- err
	}()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if _, err := os.Stat(filepath.Join(dir, "ready")); err == nil {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("probe exited before readiness: %v", err)
		case <-tick.C:
		}
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled measurement: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "quit")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("measurement received SIGQUIT: %v", err)
	}
}
