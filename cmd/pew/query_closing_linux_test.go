//go:build linux

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/greatliontech/pew/internal/run"
	"github.com/greatliontech/pew/internal/store"
)

// The publishing reader, not an earlier already-contained engine query, meets
// the pipe holder. The first completed arm remains durable when the second ends.
func TestClosingQueryExpiryKeepsThePublishedPrefix(t *testing.T) {
	if testing.Short() {
		t.Skip("executes two arms and expires the publishing query")
	}
	realGo, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	shim := t.TempDir()
	arm, pidFile := filepath.Join(shim, "armed"), filepath.Join(shim, "pid")
	script := "#!/bin/sh\nif [ \"$1 $2\" = 'env -json' ] && [ -e \"$PEW_QUERY_ARM\" ]; then\n sleep 60 &\n child=$!\n printf '%s\\n' \"$child\" > \"$PEW_QUERY_PID\"\n wait\nelse\n exec \"$PEW_REAL_GO\" \"$@\"\nfi\n"
	writeFile(t, filepath.Join(shim, "go"), script)
	if err := os.Chmod(filepath.Join(shim, "go"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PEW_REAL_GO", realGo)
	t.Setenv("PEW_QUERY_ARM", arm)
	t.Setenv("PEW_QUERY_PID", pidFile)
	t.Setenv("PATH", shim+string(os.PathListSeparator)+os.Getenv("PATH"))
	dir := twoArmFixture(t)
	withWorkingDir(t, dir)
	ctx, stop := startReporter(t.Context(), &bytes.Buffer{}, 0)
	defer stop()
	setPhaseHook(ctx, func(phase string) {
		if phase == "publishing example.com/arms.BenchmarkSecond" {
			if err := os.WriteFile(arm, nil, 0o600); err != nil {
				t.Error(err)
			}
		}
	})
	st := store.New(filepath.Join(dir, "benchmarks"))
	var out, log bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- runRun(ctx, &out, &log, runConfig{all: true, benchDir: st.Root, opts: run.Options{Count: 1, Benchtime: "1x", Bench: "."}, writeGateBound: 5 * time.Second}, []string{"."})
	}()
	var child *os.Process
	limit := time.After(30 * time.Second)
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
waitForChild:
	for {
		select {
		case err := <-done:
			t.Fatalf("publishing query not reached: %v\n%s\n%s", err, &out, &log)
		case <-limit:
			t.Fatal("publishing query did not create its descendant")
		case <-tick.C:
			data, err := os.ReadFile(pidFile)
			if err != nil {
				continue
			}
			pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
			if err != nil || pid <= 0 {
				continue
			}
			child, err = os.FindProcess(pid)
			if err != nil {
				t.Fatal(err)
			}
			defer child.Release()
			defer child.Kill()
			break waitForChild
		}
	}
	select {
	case err = <-done:
	case <-time.After(8 * time.Second):
		_ = child.Kill()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("cleanup did not drain the query")
		}
		t.Fatal("publishing query escaped its closing bound")
	}
	if err == nil || !strings.Contains(out.String(), "write gate exceeded 5s") {
		t.Fatalf("wrong closing account: %v\n%s\n%s", err, &out, &log)
	}
	if _, err := st.ReadBytes(store.Key{Bench: "BenchmarkFirst"}); err != nil {
		t.Fatalf("lost completed prefix: %v", err)
	}
	if _, err := st.ReadBytes(store.Key{Bench: "BenchmarkSecond"}); !os.IsNotExist(err) {
		t.Fatalf("expired arm published: %v", err)
	}
}
