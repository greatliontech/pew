package run

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Cancellation kills the whole process group: a grandchild the command
// spawned (the test binary `go test` launches) dies with it, so the
// wait returns within the grace and nothing keeps burning the host
// (REQ-pew-interruption).
func TestCancellationKillsTheProcessGroup(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	marker := t.TempDir() + "/grandchild.pid"
	ctx, cancel := context.WithCancel(context.Background())
	start := time.Now()
	go func() {
		time.Sleep(300 * time.Millisecond)
		cancel()
	}()
	// The shell backgrounds a grandchild and records ITS pid ($!), then
	// sleeps well past the grace — the shape of `go test` and the test
	// binary it spawns.
	_, err := ExecuteBinaryContext(ctx, t.TempDir(), "", os.Environ(), "sh", []string{"-c", "sleep 30 & echo $! > " + marker + "; sleep 30"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled command = %v; want context.Canceled", err)
	}
	if elapsed := time.Since(start); elapsed > killGrace+2*time.Second {
		t.Fatalf("the cancelled command took %s to return; the grandchild held the pipe", elapsed)
	}
	data, readErr := os.ReadFile(marker)
	if readErr != nil {
		t.Fatalf("grandchild marker: %v", readErr)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	if pid <= 0 || pid == os.Getpid() {
		t.Fatalf("marker holds %q; want the grandchild's pid", data)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat("/proc/" + strconv.Itoa(pid)); err != nil {
			return
		}
		if state, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat"); err == nil && strings.Contains(string(state), ") Z ") {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("grandchild %d survived the cancellation", pid)
}
