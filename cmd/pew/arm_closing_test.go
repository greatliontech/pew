package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/greatliontech/pew/internal/run"
	"github.com/greatliontech/pew/internal/store"
)

func TestInterruptedArmNamesItsDetachedClosingFailure(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("executes producing benchmark arms")
	}
	for _, mode := range []string{"expiry", "source drift"} {
		t.Run(mode, func(t *testing.T) {
			dir := twoArmFixture(t)
			ctx, cancel := context.WithCancel(withWorkingDir(t, dir))
			defer cancel()
			st := store.New(filepath.Join(t.TempDir(), "recordings"))
			writeStatRecording(t, st, "", "BenchmarkFirst", 42)
			key := store.Key{Bench: "BenchmarkFirst"}
			before, err := st.ReadBytes(key)
			if err != nil {
				t.Fatal(err)
			}
			rc := runConfig{all: true, benchDir: st.Root, opts: run.Options{Count: 1, Benchtime: "1x", Bench: "."}, beforePersist: func(string) {
				cancel()
				if mode == "source drift" {
					writeFile(t, filepath.Join(dir, "changed_test.go"), "package arms\nfunc AddedDuringClose() {}\n")
				}
			}}
			want := "analysis view changed"
			if mode == "expiry" {
				rc.writeGateBound = time.Nanosecond
				want = "write gate exceeded 1ns"
			}
			var out, diagnostics bytes.Buffer
			err = runRun(ctx, &out, &diagnostics, rc, []string{"."})
			var interrupted *interruptedError
			if !errors.As(err, &interrupted) || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "1 measured but not published, 1 not measured") {
				t.Fatalf("closing cause lost behind interruption: %v\n%s\n%s", err, &out, &diagnostics)
			}
			after, err := st.ReadBytes(key)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("failed closing replaced the prior recording: %v", err)
			}
		})
	}
}

func TestABArtifactClosingStopsBeforeInstallAndKeepsCommittedState(t *testing.T) {
	t.Parallel()
	for _, stage := range []string{"staged", "committed"} {
		t.Run(stage, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "ab.txt")
			writeFile(t, path, "prior bytes\n")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			a := &abArtifact{path: path, observeWrite: func(got string) {
				if got == stage {
					cancel()
				}
			}}
			block := abBlock{pkg: "example.com/ab", side: "A", raw: []byte("BenchmarkOne-2 1 5 ns/op\n")}
			err := a.publishBlocks(ctx, block)
			data, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if stage == "staged" {
				if !errors.Is(err, context.Canceled) || string(data) != "prior bytes\n" || len(a.blocks) != 0 {
					t.Fatalf("expired publication escaped or stayed queued: %q, %v, %d blocks", data, err, len(a.blocks))
				}
				// A later successful side cannot publish the refused block.
				a.observeWrite = nil
				if err := a.publishBlocks(t.Context(), abBlock{pkg: "example.com/ab", side: "B", raw: []byte("BenchmarkTwo-2 1 6 ns/op\n")}); err != nil {
					t.Fatal(err)
				}
				data, err = os.ReadFile(path)
				if err != nil || bytes.Contains(data, []byte("BenchmarkOne")) || !bytes.Contains(data, []byte("BenchmarkTwo")) {
					t.Fatalf("refused side leaked through later publication: %s, %v", data, err)
				}
			} else if err != nil || !bytes.Contains(data, []byte("BenchmarkOne")) || len(a.blocks) != 1 {
				t.Fatalf("late cancellation discarded committed state: %s, %v", data, err)
			}
		})
	}
}

func TestArmProgressFollowsItsOwnedStageBoundaries(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("executes two independently closed arms")
	}
	dir := twoArmFixture(t)
	ctx, stop := startReporter(withWorkingDir(t, dir), &bytes.Buffer{}, 0)
	defer stop()
	var stages []string
	setPhaseHook(ctx, func(phase string) {
		if strings.HasPrefix(phase, "preparing example.com/arms arm ") || strings.HasPrefix(phase, "measuring example.com/arms.") || strings.HasPrefix(phase, "finalizing example.com/arms.") || strings.HasPrefix(phase, "publishing example.com/arms.") {
			stages = append(stages, phase)
		}
	})
	entered := 0
	rc := runConfig{all: true, benchDir: filepath.Join(dir, "benchmarks"), opts: run.Options{Count: 1, Benchtime: "1x", Bench: "."}, binary: func(ctx context.Context, dir, pin string, env testEnvironmentValue, binary string, args []string) ([]byte, error) {
		entered++
		r := ctx.Value(reporterKey{}).(*reporter)
		r.mu.Lock()
		phase := r.phase
		r.mu.Unlock()
		if !strings.HasPrefix(phase, "measuring example.com/arms.") || !strings.HasSuffix(phase, fmt.Sprintf("arm %d/2", entered)) {
			t.Fatalf("actual execution attributed to %q", phase)
		}
		return run.ExecuteBinaryContext(ctx, dir, pin, env, binary, args)
	}}
	if err := runRun(ctx, &bytes.Buffer{}, &bytes.Buffer{}, rc, []string{"."}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"preparing example.com/arms arm 1/2", "measuring example.com/arms.BenchmarkFirst arm 1/2", "finalizing example.com/arms.BenchmarkFirst", "publishing example.com/arms.BenchmarkFirst",
		"preparing example.com/arms arm 2/2", "measuring example.com/arms.BenchmarkSecond arm 2/2", "finalizing example.com/arms.BenchmarkSecond", "publishing example.com/arms.BenchmarkSecond",
	}
	if !slices.Equal(stages, want) {
		t.Fatalf("arm stages = %v, want %v", stages, want)
	}
}

func TestOrdinaryArmRefusesZeroExitWithoutHarnessCompletion(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("executes a test binary which never enters the test harness")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/noharness\n\ngo 1.26\n")
	writeFile(t, filepath.Join(dir, "bench_test.go"), `package noharness
import ("fmt"; "testing")
func BenchmarkOne(b *testing.B) {}
func TestMain(m *testing.M) { fmt.Println("BenchmarkOne-2 1 5 ns/op") }
`)
	commitFixture(t, dir)
	ctx := withWorkingDir(t, dir)
	st := store.New(filepath.Join(t.TempDir(), "recordings"))
	writeStatRecording(t, st, "", "BenchmarkOne", 99)
	key := store.Key{Bench: "BenchmarkOne"}
	before, err := st.ReadBytes(key)
	if err != nil {
		t.Fatal(err)
	}
	var out, log bytes.Buffer
	err = runRun(ctx, &out, &log, runConfig{all: true, benchDir: st.Root, opts: run.Options{Count: 1, Benchtime: "1x", Bench: "."}}, []string{"."})
	if err == nil || !strings.Contains(out.String(), "benchmark harness completion missing") || strings.Contains(out.String(), "recorded     ") {
		t.Fatalf("zero exit authenticated a missing harness: %v\n%s\n%s", err, &out, &log)
	}
	after, err := st.ReadBytes(key)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("uncompleted harness replaced prior recording: %v", err)
	}
}
