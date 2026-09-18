package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"

	"github.com/greatliontech/gofresh/guard"
	"github.com/greatliontech/pew/internal/compare"
	"github.com/greatliontech/pew/internal/run"
	"github.com/greatliontech/pew/internal/store"
)

// A cancellation that lands inside a package's engine construction —
// the toolchain sample under the verb's context observes it — ends
// run, status, and stat with their interruption report and exit 130,
// never as that package's own failure row or a bare tool error
// (REQ-pew-interruption: every stage on the verb's path reports the
// same way). ab's preparation observes it in the guard capture.
func TestVerbsReportACancelledEngineBuildAsInterruption(t *testing.T) {
	if testing.Short() {
		t.Skip("lists fixture modules through the toolchain")
	}
	orig := goVersionSampler
	t.Cleanup(func() { goVersionSampler = orig })
	var cancel context.CancelFunc
	goVersionSampler = func(ctx context.Context, _ string, _ []string) (string, error) {
		cancel() // the operator interrupts while the sample runs
		return "", ctx.Err()
	}
	assertInterrupted := func(name string, err error, want string) {
		t.Helper()
		var stopped *interruptedError
		if !errors.As(err, &stopped) || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s interrupted during its engine construction = %v; want the interruption report %q", name, err, want)
		}
		if exitCode(err) != 130 {
			t.Fatalf("%s exit code %d; want 130", name, exitCode(err))
		}
	}
	var w, ew bytes.Buffer

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/stage\n\ngo 1.24\n")
	writeFile(t, filepath.Join(dir, "pkg", "pkg.go"), "package pkg\n")
	writeFile(t, filepath.Join(dir, "pkg", "pkg_test.go"), "package pkg\n\nimport \"testing\"\n\nfunc BenchmarkStage(b *testing.B) {}\n")
	withWorkingDir(t, dir)
	var ctx context.Context
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	assertInterrupted("run", runRun(ctx, &w, &ew, runConfig{benchDir: filepath.Join(dir, "b"), opts: run.Options{Count: 1, Bench: "."}}, []string{"./..."}), "interrupted while preparing example.com/stage/pkg")
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	assertInterrupted("status", runStatus(ctx, &w, filepath.Join(dir, "b"), "", false, false, false, []string{"./..."}), "status: interrupted while judging example.com/stage/pkg")
	// The same interruption landing after the construction: the sample
	// answers, so with nothing to judge no stage observes the
	// cancellation and the verb still ends by it — the last package has
	// no next iteration to notice the signal.
	answering := func(ctx context.Context, _ string, _ []string) (string, error) {
		cancel()
		return runtime.Version(), nil
	}
	goVersionSampler = answering
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	assertInterrupted("status (after the last unit)", runStatus(ctx, &w, filepath.Join(dir, "b"), "", false, false, false, []string{"./..."}), "status: interrupted after the last package")
	// With a recording to judge, the judgment re-observes the tree under
	// the ended context and reports the interruption itself.
	writeStatRecording(t, store.New(filepath.Join(dir, "b")), "pkg", "BenchmarkStage", 100)
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	assertInterrupted("status (judgment)", runStatus(ctx, &w, filepath.Join(dir, "b"), "", false, false, false, []string{"./..."}), "status: interrupted while judging example.com/stage/pkg")
	goVersionSampler = func(ctx context.Context, _ string, _ []string) (string, error) {
		cancel()
		return "", ctx.Err()
	}

	statDir := t.TempDir()
	writeFile(t, filepath.Join(statDir, "go.mod"), "module example.com/statstage\n\ngo 1.26.4\n")
	writeFile(t, filepath.Join(statDir, "pkg", "pkg.go"), "package pkg\n")
	writeFile(t, filepath.Join(statDir, "pkg", "pkg_test.go"), "package pkg\n\nimport \"testing\"\n\nfunc BenchmarkStage(b *testing.B) {}\n")
	repo, err := gogit.PlainInit(statDir, false)
	if err != nil {
		t.Fatal(err)
	}
	st := store.New(filepath.Join(statDir, "benchmarks"))
	writeStatRecording(t, st, "pkg", "BenchmarkStage", 100)
	commitAll(t, repo, "base")
	withWorkingDir(t, statDir)
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	assertInterrupted("stat", runStat(ctx, &w, &ew, statConfig{benchDir: st.Root, opts: compare.DefaultOptions()}, nil), "stat: interrupted at pkg.BenchmarkStage")

	// stat: the signal lands on a named stretch — the reporter's phase is
	// the hook — inside the goflags re-derivation of the first key and,
	// with those cached, inside the second key's verdict re-observation.
	twoDir := t.TempDir()
	writeFile(t, filepath.Join(twoDir, "go.mod"), "module example.com/stattwo\n\ngo 1.26.4\n")
	writeFile(t, filepath.Join(twoDir, "pkg", "pkg.go"), "package pkg\n")
	writeFile(t, filepath.Join(twoDir, "pkg", "pkg_test.go"), "package pkg\n\nimport \"testing\"\n\nfunc BenchmarkA(b *testing.B) {}\nfunc BenchmarkB(b *testing.B) {}\n")
	twoRepo, err := gogit.PlainInit(twoDir, false)
	if err != nil {
		t.Fatal(err)
	}
	two := store.New(filepath.Join(twoDir, "benchmarks"))
	writeStatRecording(t, two, "pkg", "BenchmarkA", 100)
	writeStatRecording(t, two, "pkg", "BenchmarkB", 100)
	commitAll(t, twoRepo, "base")
	withWorkingDir(t, twoDir)
	goVersionSampler = orig
	stopReporter := startReporter(io.Discard, time.Hour)
	t.Cleanup(stopReporter)
	for _, bench := range []string{"BenchmarkA", "BenchmarkB"} {
		ctx, cancel = context.WithCancel(context.Background())
		defer cancel()
		setPhaseHook(func(phase string) {
			if strings.HasPrefix(phase, "judging pkg."+bench+" ") {
				cancel()
			}
		})
		assertInterrupted("stat at "+bench, runStat(ctx, &w, &ew, statConfig{benchDir: two.Root, opts: compare.DefaultOptions()}, nil), "stat: interrupted at pkg."+bench)
	}
	setPhaseHook(nil)

	// gc: the signal lands while the module is resolved for a module
	// that lists no package at all.
	gcDir := t.TempDir()
	writeFile(t, filepath.Join(gcDir, "go.mod"), "module example.com/gcmod\n\ngo 1.24\n")
	withWorkingDir(t, gcDir)
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	setPhaseHook(func(phase string) {
		if phase == "resolving the module" {
			cancel()
		}
	})
	assertInterrupted("gc", runGC(ctx, &w, filepath.Join(gcDir, "b")), "gc: interrupted while resolving the module")
	setPhaseHook(nil)
	goVersionSampler = func(ctx context.Context, _ string, _ []string) (string, error) {
		cancel()
		return "", ctx.Err()
	}

	abDir := abFixtureRepo(t)
	withWorkingDir(t, abDir)
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	ac := abConfig{
		bench: ".", count: 1, benchtime: "1x", ref: "HEAD",
		build: func(string, []string, []string) error { return nil },
		guards: func(ctx context.Context, _, _ string, _ bool, _ []string) (guard.Guards, error) {
			cancel() // the operator interrupts while the guards are captured
			return guard.Guards{}, ctx.Err()
		},
	}
	assertInterrupted("ab", runAB(ctx, &w, &ew, ac, []string{"./..."}), "ab: interrupted while preparing")
}

// An interruption landing between an arm's measurement and its write
// keeps the measured arm: the write gate runs bounded and detached
// from the verb's context, so the gate's own evidence decides the
// write and the verb then ends with the arm reported as recorded
// (REQ-pew-unit-persistence, REQ-pew-interruption).
func TestMeasuredArmPersistsUnderAnInterruptedGate(t *testing.T) {
	if testing.Short() {
		t.Skip("loads a fixture package through the toolchain with the execute seam stubbed")
	}
	dir := twoArmFixture(t)
	withWorkingDir(t, dir)
	benchDir := filepath.Join(dir, "benchmarks")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rc := runConfig{
		benchDir: benchDir,
		opts:     run.Options{Count: 1, Benchtime: "1x", Bench: "."},
		throttle: func() run.ThrottleSnapshot { return run.ThrottleSnapshot{"c0": 1} },
		execute:  armStub(nil),
		beforePersist: func(bench string) {
			if bench == "BenchmarkFirst" {
				cancel() // the operator interrupts after the arm measured, before its write
			}
		},
	}
	var out bytes.Buffer
	err := runRun(ctx, &out, &bytes.Buffer{}, rc, []string{"."})
	if err == nil || !strings.Contains(err.Error(), "interrupted: 1 recorded, 1 not measured in example.com/arms") {
		t.Fatalf("runRun = %v; want the measured arm recorded and the interruption reported\n%s", err, out.String())
	}
	st := store.New(benchDir)
	if _, err := st.Read("", "BenchmarkFirst", ""); err != nil {
		t.Fatalf("the arm measured before the interruption was not kept: %v", err)
	}
	if _, err := st.Read("", "BenchmarkSecond", ""); !errors.Is(err, store.ErrNotRecorded) {
		t.Fatalf("an arm after the interruption recorded: %v", err)
	}
}

// The toolchain sample runs under the caller's context: a cancelled
// context ends it with the cancellation, so an interruption during
// the sample is the verb's and never a memoized fact about the module.
func TestGoVersionSampleHonorsTheCallersContext(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/sample\n\ngo 1.24\n")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := sampleGoVersion(ctx, dir, os.Environ()); !errors.Is(err, context.Canceled) {
		t.Fatalf("sample under a cancelled context = %v; want the cancellation", err)
	}
}

// setPhaseHook installs the active reporter's phase observer under its
// lock — the field's guard is the reporter's mutex.
func setPhaseHook(f func(string)) {
	activeMu.Lock()
	r := active
	activeMu.Unlock()
	r.mu.Lock()
	r.onPhase = f
	r.mu.Unlock()
}

// cancelOnWrite cancels the verb's context at its first output write —
// the shape of a signal landing after the last unit completed, as the
// verb writes its ending; every consultation before the write sees a
// live context.
type cancelOnWrite struct {
	w      io.Writer
	cancel context.CancelFunc
}

func (c cancelOnWrite) Write(p []byte) (int, error) {
	c.cancel()
	return c.w.Write(p)
}

// An interruption received after a verb's last unit completed is still
// reported: nothing was cut short, everything measured is kept, and
// the verb ends by the signal with exit 130 rather than answering as
// if none had come (REQ-pew-interruption).
func TestVerbsEndByAnInterruptionReceivedAfterTheLastUnit(t *testing.T) {
	if testing.Short() {
		t.Skip("loads fixture packages through the toolchain with the execute seams stubbed")
	}
	assertInterrupted := func(name string, err error, want string) {
		t.Helper()
		var stopped *interruptedError
		if !errors.As(err, &stopped) || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s interrupted after its last unit = %v; want the interruption report %q", name, err, want)
		}
		if exitCode(err) != 130 {
			t.Fatalf("%s exit code %d; want 130", name, exitCode(err))
		}
	}
	var w, ew bytes.Buffer

	// run: the signal lands during the last arm's write gate — the arm
	// is written (the gate runs detached) and the verb ends by the signal.
	dir := twoArmFixture(t)
	withWorkingDir(t, dir)
	benchDir := filepath.Join(dir, "benchmarks")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rc := runConfig{
		benchDir: benchDir,
		opts:     run.Options{Count: 1, Benchtime: "1x", Bench: "."},
		throttle: func() run.ThrottleSnapshot { return run.ThrottleSnapshot{"c0": 1} },
		execute:  armStub(nil),
		beforePersist: func(bench string) {
			if bench == "BenchmarkSecond" {
				cancel()
			}
		},
	}
	assertInterrupted("run", runRun(ctx, &w, &ew, rc, []string{"."}), "interrupted after the last package; every recorded arm is kept")
	st := store.New(benchDir)
	for _, bench := range []string{"BenchmarkFirst", "BenchmarkSecond"} {
		if _, err := st.Read("", bench, ""); err != nil {
			t.Fatalf("%s measured before the interruption was not kept: %v", bench, err)
		}
	}

	// stat: the signal lands after the last comparison completed.
	statDir := t.TempDir()
	writeFile(t, filepath.Join(statDir, "go.mod"), "module example.com/statend\n\ngo 1.26.4\n")
	writeFile(t, filepath.Join(statDir, "pkg", "pkg.go"), "package pkg\n")
	writeFile(t, filepath.Join(statDir, "pkg", "pkg_test.go"), "package pkg\n\nimport \"testing\"\n\nfunc BenchmarkEnd(b *testing.B) {}\n")
	repo, err := gogit.PlainInit(statDir, false)
	if err != nil {
		t.Fatal(err)
	}
	sst := store.New(filepath.Join(statDir, "benchmarks"))
	writeStatRecording(t, sst, "pkg", "BenchmarkEnd", 100)
	commitAll(t, repo, "base")
	withWorkingDir(t, statDir)
	// Every stage of the comparison observes a cancellation and reports
	// it, so the end check is reached only by a signal landing after the
	// last comparison's work — as the table is written.
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	assertInterrupted("stat", runStat(ctx, cancelOnWrite{w: &w, cancel: cancel}, &ew, statConfig{benchDir: sst.Root, opts: compare.DefaultOptions()}, nil), "stat: interrupted after the last comparison")

	// ab: the signal lands during the last iteration's second side; the
	// pair completes into the artifact and the verb ends by the signal.
	abDir := abFixtureRepo(t)
	withWorkingDir(t, abDir)
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	runs := 0
	ac := abConfig{
		bench: ".", count: 1, ref: "HEAD", out: filepath.Join(t.TempDir(), "ab.txt"),
		throttle: func() run.ThrottleSnapshot { return run.ThrottleSnapshot{} },
		build:    func(string, []string, []string) error { return nil },
		execute: func(_ string, _ string, _ []string, _ string, _ []string) ([]byte, error) {
			runs++
			if runs == 2 {
				cancel()
			}
			return []byte("goos: linux\ngoarch: amd64\npkg: example.com/abfix/p\ncpu: T\nBenchmarkWork-8 1 5 ns/op\nPASS\n"), nil
		},
		guards: func(context.Context, string, string, bool, []string) (guard.Guards, error) {
			return guard.Guards{Toolchain: "go1", BuildConfig: "b", Machine: "m", RuntimeConfig: "r"}, nil
		},
	}
	assertInterrupted("ab", runAB(ctx, &w, &ew, ac, []string{"./p"}), "ab: interrupted after the last package")

	// gc: the signal lands as the verb writes its ending; nothing before
	// the write observed it.
	gcDir := t.TempDir()
	writeFile(t, filepath.Join(gcDir, "go.mod"), "module example.com/gcend\n\ngo 1.24\n")
	writeFile(t, filepath.Join(gcDir, "pkg", "pkg.go"), "package pkg\n")
	withWorkingDir(t, gcDir)
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	assertInterrupted("gc", runGC(ctx, cancelOnWrite{w: &w, cancel: cancel}, filepath.Join(gcDir, "b")), "gc: interrupted after the last store")
}

// A cancellation landing inside a stage of a package's measurement —
// ab's side process, run's warm-up build, a measured arm's gate that
// exceeded its bound — reports as the verb's interruption, never as
// that package's failure row, and a gate the bound expired names the
// gate (REQ-pew-interruption, REQ-pew-unit-persistence).
func TestMeasurementStagesReportTheirCancellationAsInterruption(t *testing.T) {
	if testing.Short() {
		t.Skip("loads fixture packages through the toolchain with the execute seams stubbed")
	}
	assertInterrupted := func(name string, err error, want string) {
		t.Helper()
		var stopped *interruptedError
		if !errors.As(err, &stopped) || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s cancelled inside a measurement stage = %v; want the interruption report %q", name, err, want)
		}
		if exitCode(err) != 130 {
			t.Fatalf("%s exit code %d; want 130", name, exitCode(err))
		}
	}
	var w, ew bytes.Buffer

	// ab: the second pair's side A, then its side B, is killed by the
	// signal — what the real executor returns is the context's error.
	for _, killed := range []int{3, 4} {
		abDir := abFixtureRepo(t)
		withWorkingDir(t, abDir)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		runs := 0
		out := filepath.Join(t.TempDir(), "ab.txt")
		ac := abConfig{
			bench: ".", count: 2, ref: "HEAD", out: out,
			throttle: func() run.ThrottleSnapshot { return run.ThrottleSnapshot{} },
			build:    func(string, []string, []string) error { return nil },
			execute: func(_ string, _ string, _ []string, _ string, _ []string) ([]byte, error) {
				runs++
				if runs == killed {
					cancel()
					return nil, context.Canceled
				}
				return []byte("goos: linux\ngoarch: amd64\npkg: example.com/abfix/p\ncpu: T\nBenchmarkWork-8 1 5 ns/op\nPASS\n"), nil
			},
			guards: func(context.Context, string, string, bool, []string) (guard.Guards, error) {
				return guard.Guards{Toolchain: "go1", BuildConfig: "b", Machine: "m", RuntimeConfig: "r"}, nil
			},
		}
		assertInterrupted(fmt.Sprintf("ab (side %d killed)", killed), runAB(ctx, &w, &ew, ac, []string{"./p"}), "interrupted after 1 of 2 iterations (the artifact holds them)")
		if data, err := os.ReadFile(out); err != nil || strings.Count(string(data), "BenchmarkWork-8") != 2 {
			t.Fatalf("artifact after the interruption = %q, %v; want the completed pair's two rows", data, err)
		}
	}

	// run: the signal lands during the package's warm-up build, before
	// any arm — the package is not measured, not failed.
	dir := twoArmFixture(t)
	withWorkingDir(t, dir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rc := runConfig{
		benchDir: filepath.Join(dir, "benchmarks"),
		opts:     run.Options{Count: 1, Benchtime: "1x", Bench: "."},
		throttle: func() run.ThrottleSnapshot { return run.ThrottleSnapshot{"c0": 1} },
		execute: func(_ string, _ string, _ []string, args []string) ([]byte, error) {
			for _, a := range args {
				if a == "-c" {
					cancel()
					return nil, context.Canceled
				}
			}
			t.Fatal("an arm ran after the warm-up build was interrupted")
			return nil, nil
		},
	}
	w.Reset()
	assertInterrupted("run", runRun(ctx, &w, &ew, rc, []string{"."}), "interrupted while measuring example.com/arms (package 1/1)")
	if strings.Contains(w.String(), "error ") {
		t.Fatalf("an interrupted package was reported as failed:\n%s", w.String())
	}

	// run: a measured arm whose write gate exceeded its bound is
	// discarded by the gate, naming it — not by the verb's context.
	bound := armWriteGateBound
	armWriteGateBound = time.Nanosecond
	defer func() { armWriteGateBound = bound }()
	dir = twoArmFixture(t)
	withWorkingDir(t, dir)
	rc = runConfig{
		benchDir: filepath.Join(dir, "benchmarks"),
		opts:     run.Options{Count: 1, Benchtime: "1x", Bench: "."},
		throttle: func() run.ThrottleSnapshot { return run.ThrottleSnapshot{"c0": 1} },
		execute:  armStub(nil),
	}
	w.Reset()
	err := runRun(context.Background(), &w, &ew, rc, []string{"."})
	if err == nil || !strings.Contains(w.String(), "the arm's write gate exceeded 1ns") {
		t.Fatalf("expired gate = %v; output:\n%s\nwant the gate named", err, w.String())
	}
}
