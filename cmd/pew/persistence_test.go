package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/guard"
	"github.com/greatliontech/pew/internal/compare"
	"github.com/greatliontech/pew/internal/run"
	"github.com/greatliontech/pew/internal/store"
)

// twoArmFixture is a committed module with two benchmarks.
func twoArmFixture(t *testing.T) (dir string) {
	t.Helper()
	dir = t.TempDir()
	files := map[string]string{
		"go.mod":        "module example.com/arms\n\ngo 1.26.4\n",
		"bench_test.go": "package arms\n\nimport \"testing\"\n\nfunc BenchmarkFirst(b *testing.B) {}\nfunc BenchmarkSecond(b *testing.B) {}\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	commitFixture(t, dir)
	return dir
}

// armStub is an execute seam that fakes a measured arm and lets the
// test act between arms.
func armStub(between func(arm int)) func(moduleDir, pin string, env, args []string) ([]byte, error) {
	arms := 0
	return func(moduleDir, pin string, env, args []string) ([]byte, error) {
		for _, a := range args {
			if a == "-c" {
				return nil, nil
			}
		}
		arms++
		if between != nil {
			between(arms)
		}
		name := "BenchmarkFirst"
		for _, a := range args {
			if strings.Contains(a, "BenchmarkSecond") {
				name = "BenchmarkSecond"
			}
		}
		return []byte("goos: linux\ngoarch: amd64\npkg: example.com/arms\ncpu: T\n" + name + "-8 1 5 ns/op\nPASS\n"), nil
	}
}

// Each arm persists the moment it measured, behind its own write gate:
// when HEAD moves during the second arm, the first arm's recording
// stands and the second is refused (REQ-pew-unit-persistence).
func TestRunPersistsEachArmAsItMeasures(t *testing.T) {
	if testing.Short() {
		t.Skip("loads a fixture package through the toolchain with the execute seam stubbed")
	}
	dir := twoArmFixture(t)
	withWorkingDir(t, dir)
	benchDir := filepath.Join(dir, "benchmarks")
	rc := runConfig{
		benchDir: benchDir,
		opts:     run.Options{Count: 1, Benchtime: "1x", Bench: "."},
		throttle: func() run.ThrottleSnapshot { return run.ThrottleSnapshot{"c0": 1} },
		execute: armStub(func(arm int) {
			if arm != 2 {
				return
			}
			// HEAD moves under the second arm.
			if err := os.WriteFile(filepath.Join(dir, "moved.txt"), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
			raw, err := gogit.PlainOpen(dir)
			if err != nil {
				t.Fatal(err)
			}
			wt, err := raw.Worktree()
			if err != nil {
				t.Fatal(err)
			}
			if err := wt.AddGlob("."); err != nil {
				t.Fatal(err)
			}
			if _, err := wt.Commit("moved", &gogit.CommitOptions{Author: &object.Signature{Name: "t", Email: "t@example.invalid", When: time.Unix(2, 0)}}); err != nil {
				t.Fatal(err)
			}
		}),
	}
	var out bytes.Buffer
	err := runRun(context.Background(), &out, &bytes.Buffer{}, rc, []string{"."})
	// The second arm refuses on its own moved state bracket; a batch
	// write at the end would have refused the whole package on HEAD, the
	// first arm's measurement with it.
	if err == nil || !strings.Contains(out.String(), "(1 recorded)") || !strings.Contains(out.String(), "recorded     example.com/arms.BenchmarkFirst") {
		t.Fatalf("runRun = %v; want the second arm refused with one arm recorded\n%s", err, out.String())
	}
	st := store.New(benchDir)
	if _, err := st.Read("", "BenchmarkFirst", ""); err != nil {
		t.Fatalf("the first arm's recording is missing: %v", err)
	}
	if _, err := st.Read("", "BenchmarkSecond", ""); !errors.Is(err, store.ErrNotRecorded) {
		t.Fatalf("the refused arm recorded anyway: %v", err)
	}
}

// An interruption stops before the next arm: the arms recorded so far
// stand and the report says what was kept (REQ-pew-interruption).
func TestRunInterruptedKeepsRecordedArms(t *testing.T) {
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
		execute: armStub(func(arm int) {
			if arm == 2 {
				cancel() // the operator interrupts while the second arm runs
			}
		}),
	}
	var out bytes.Buffer
	err := runRun(ctx, &out, &bytes.Buffer{}, rc, []string{"."})
	// The arm in flight is lost (its process is killed); the arm that
	// finished before the interrupt is kept and named.
	if err == nil || !strings.Contains(err.Error(), "interrupted: 1 recorded, 1 not measured in example.com/arms") {
		t.Fatalf("runRun = %v; want the interruption report naming the kept arm\n%s", err, out.String())
	}
	st := store.New(benchDir)
	if _, err := st.Read("", "BenchmarkFirst", ""); err != nil {
		t.Fatalf("the finished arm was not kept: %v", err)
	}
	if _, err := st.Read("", "BenchmarkSecond", ""); !errors.Is(err, store.ErrNotRecorded) {
		t.Fatalf("an arm after the interruption recorded: %v", err)
	}
}

// ab writes its artifact once per completed iteration pair, so an
// interrupted comparison keeps the iterations it finished
// (REQ-pew-unit-persistence, REQ-pew-interruption).
func TestABArtifactHoldsEachCompletedIteration(t *testing.T) {
	if testing.Short() {
		t.Skip("loads the fixture through the toolchain with the build and execute seams stubbed")
	}
	dir := abFixtureRepo(t)
	withWorkingDir(t, dir)
	out := filepath.Join(t.TempDir(), "ab.txt")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runs := 0
	ac := abConfig{
		bench: ".", count: 3, ref: "HEAD", out: out,
		throttle: func() run.ThrottleSnapshot { return run.ThrottleSnapshot{} },
		build:    func(string, []string, []string) error { return nil },
		execute: func(_ string, _ string, _ []string, _ string, _ []string) ([]byte, error) {
			runs++
			if runs == 2 {
				cancel() // interrupted after the first pair
			}
			return []byte("goos: linux\ngoarch: amd64\npkg: example.com/abfix/p\ncpu: T\nBenchmarkWork-8 1 5 ns/op\nPASS\n"), nil
		},
		guards: func(string, string, bool, []string) (guard.Guards, error) {
			return guard.Guards{Toolchain: "go1", BuildConfig: "b", Machine: "m", RuntimeConfig: "r"}, nil
		},
	}
	err := runAB(ctx, &bytes.Buffer{}, &bytes.Buffer{}, ac, []string{"./p"})
	if err == nil || !strings.Contains(err.Error(), "interrupted after 1 of 3 iterations") {
		t.Fatalf("runAB = %v; want the interruption report after one pair", err)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("the artifact of the completed pair is missing: %v", err)
	}
	if strings.Count(string(data), "BenchmarkWork-8") != 2 {
		t.Fatalf("artifact holds %d rows; want the completed pair's two:\n%s", strings.Count(string(data), "BenchmarkWork-8"), data)
	}
}

// gc reports each removal the moment it lands, through the store walk
// itself, so an interrupted gc has reported everything it did.
func TestGCReportsEachRemovalAsItLands(t *testing.T) {
	st := store.New(t.TempDir())
	writeRecording(t, st, "p", "BenchmarkGone", "")
	var w bytes.Buffer
	removed, _, err := gcStore(&w, st, map[string]map[string]bool{"p": {}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(removed) != 1 || !strings.Contains(w.String(), "removed      p.BenchmarkGone") {
		t.Fatalf("removal not reported by the walk itself: removed=%v out=%q", removed, w.String())
	}
}

// The reporter repeats the phase in flight on its cadence with the
// elapsed time, and the engine's detail-free keep-alives name the
// analysis stretch (REQ-pew-progress).
func TestReporterNamesThePhaseOnTheCadence(t *testing.T) {
	log := &lockedBuffer{}
	stop := startReporter(log, 5*time.Millisecond)
	reportPhase("measuring example.com/p arm 1/2")
	time.Sleep(40 * time.Millisecond)
	emitEngineDiagnostic(gofresh.Progress{Phase: "load", Package: "example.com/p"})
	time.Sleep(40 * time.Millisecond)
	stop() // joins the cadence goroutine: no line lands after it
	text := log.String()
	if !strings.Contains(text, "pew: measuring example.com/p arm 1/2 (") || !strings.Contains(text, "elapsed)") {
		t.Fatalf("no cadenced phase line:\n%s", text)
	}
	if !strings.Contains(text, "pew: analysis load example.com/p (") {
		t.Fatalf("the engine keep-alive did not name the analysis stretch:\n%s", text)
	}
	time.Sleep(30 * time.Millisecond)
	if log.String() != text {
		t.Fatal("the cadence kept printing after stop")
	}
	reportPhase("after stop")
	if log.String() != text {
		t.Fatal("a phase set after stop printed")
	}
}

// lockedBuffer is a bytes.Buffer safe for the cadence goroutine and
// the test to share.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// The per-arm write gate re-derives for the arm's own span, in the
// window after its state bracket closed: a source edit there fails the
// view's validation and a moved HEAD fails the commit check, each
// refusing that arm alone with the earlier arm kept
// (REQ-pew-unit-persistence).
func TestRunPerArmGateRefusesTheArmAlone(t *testing.T) {
	if testing.Short() {
		t.Skip("loads a fixture package through the toolchain with the execute seam stubbed")
	}
	for name, move := range map[string]func(t *testing.T, dir string){
		"source edit": func(t *testing.T, dir string) {
			if err := os.WriteFile(filepath.Join(dir, "bench_test.go"), []byte("package arms\n\nimport \"testing\"\n\nfunc BenchmarkFirst(b *testing.B) {}\nfunc BenchmarkSecond(b *testing.B) { _ = 1 }\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		},
		"HEAD moved": func(t *testing.T, dir string) {
			if err := os.WriteFile(filepath.Join(dir, "moved.txt"), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
			raw, err := gogit.PlainOpen(dir)
			if err != nil {
				t.Fatal(err)
			}
			wt, err := raw.Worktree()
			if err != nil {
				t.Fatal(err)
			}
			if err := wt.AddGlob("."); err != nil {
				t.Fatal(err)
			}
			if _, err := wt.Commit("moved", &gogit.CommitOptions{Author: &object.Signature{Name: "t", Email: "t@example.invalid", When: time.Unix(2, 0)}}); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir := twoArmFixture(t)
			withWorkingDir(t, dir)
			benchDir := filepath.Join(dir, "benchmarks")
			rc := runConfig{
				benchDir: benchDir,
				opts:     run.Options{Count: 1, Benchtime: "1x", Bench: "."},
				throttle: func() run.ThrottleSnapshot { return run.ThrottleSnapshot{"c0": 1} },
				execute:  armStub(nil),
				beforePersist: func(bench string) {
					if bench == "BenchmarkSecond" {
						move(t, dir)
					}
				},
			}
			var out bytes.Buffer
			err := runRun(context.Background(), &out, &bytes.Buffer{}, rc, []string{"."})
			if err == nil || !strings.Contains(out.String(), "BenchmarkSecond:") || !strings.Contains(out.String(), "(1 recorded)") {
				t.Fatalf("runRun = %v; want the second arm refused at its own gate with the first kept\n%s", err, out.String())
			}
			st := store.New(benchDir)
			if _, err := st.Read("", "BenchmarkFirst", ""); err != nil {
				t.Fatalf("the first arm's recording is missing: %v", err)
			}
			if _, err := st.Read("", "BenchmarkSecond", ""); !errors.Is(err, store.ErrNotRecorded) {
				t.Fatalf("the refused arm recorded anyway: %v", err)
			}
		})
	}
}

// An arm that will not run pays none of its preparation: after an
// interruption lands, the next arm is never entered.
func TestRunInterruptedArmsAreNeverEntered(t *testing.T) {
	if testing.Short() {
		t.Skip("loads a fixture package through the toolchain with the execute seam stubbed")
	}
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":        "module example.com/arms\n\ngo 1.26.4\n",
		"bench_test.go": "package arms\n\nimport \"testing\"\n\n//pew:scratch pewtmp\nfunc BenchmarkFirst(b *testing.B) {}\nfunc BenchmarkSecond(b *testing.B) {}\nfunc BenchmarkThird(b *testing.B) {}\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	commitFixture(t, dir)
	withWorkingDir(t, dir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// A leftover matching the scratch directive, planted by the first
	// arm: the next arm's own preparation would sweep it, so its survival
	// proves the arm was never entered. The interrupt lands in the
	// instant between two iterations — the "recorded" line the first
	// arm's persist writes is the last statement before the loop turns.
	leftover := filepath.Join(dir, "pewtmp-planted")
	arms := 0
	rc := runConfig{
		benchDir: filepath.Join(dir, "benchmarks"),
		opts:     run.Options{Count: 1, Benchtime: "1x", Bench: "."},
		throttle: func() run.ThrottleSnapshot { return run.ThrottleSnapshot{"c0": 1} },
		execute: armStub(func(arm int) {
			arms = arm
			if arm == 1 {
				if err := os.MkdirAll(leftover, 0o755); err != nil {
					t.Fatal(err)
				}
			}
		}),
	}
	out := &cancelOnRecorded{cancel: cancel}
	err := runRun(ctx, out, &bytes.Buffer{}, rc, []string{"."})
	if err == nil || !strings.Contains(err.Error(), "interrupted: 1 recorded, 2 not measured") {
		t.Fatalf("runRun = %v; want the kept arm and the two unmeasured counted\n%s", err, out.b.String())
	}
	if arms != 1 {
		t.Fatalf("%d arms entered after the interruption; want only the first", arms)
	}
	if _, err := os.Stat(leftover); err != nil {
		t.Fatalf("the next arm's preparation ran after the interruption (its sweep removed the planted leftover): %v", err)
	}
}

// cancelOnRecorded cancels a context the first time a "recorded" line
// is written — after the arm's persist, before the loop turns.
type cancelOnRecorded struct {
	b      bytes.Buffer
	cancel context.CancelFunc
	done   bool
}

func (c *cancelOnRecorded) Write(p []byte) (int, error) {
	if !c.done && bytes.Contains(p, []byte("recorded     ")) {
		c.done = true
		c.cancel()
	}
	return c.b.Write(p)
}

// stat binds the same interruption: a cancelled context ends it before
// its next module with a report, never a silent hang through the
// history scan and the per-key walk (REQ-pew-interruption).
func TestStatInterruptedEndsWithAReport(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/statstop\n\ngo 1.26.4\n")
	writeFile(t, filepath.Join(dir, "pkg", "pkg.go"), "package pkg\n")
	repo, err := gogit.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	st := store.New(filepath.Join(dir, "benchmarks"))
	writeStatRecording(t, st, "pkg", "BenchmarkStop", 100)
	commitAll(t, repo, "base")
	withWorkingDir(t, dir)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out, errOut bytes.Buffer
	err = runStat(ctx, &out, &errOut, statConfig{benchDir: st.Root, opts: compare.DefaultOptions()}, nil)
	var stopped *interruptedError
	if !errors.As(err, &stopped) || !strings.Contains(err.Error(), "stat: interrupted") {
		t.Fatalf("runStat under a cancelled context = %v; want the interruption report", err)
	}
	if exitCode(err) != 130 {
		t.Fatalf("exit code %d for an interruption; want 130", exitCode(err))
	}
}
