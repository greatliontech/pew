package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/pew/internal/gitblob"
	runpkg "github.com/greatliontech/pew/internal/run"
	"github.com/greatliontech/pew/internal/store"
	"golang.org/x/perf/benchfmt"
)

func TestRequiredBenchmarksIncludesCurrentImpureSelection(t *testing.T) {
	all := []string{"BenchmarkA", "BenchmarkB", "BenchmarkC"}
	got := requiredBenchmarks(all, []string{"BenchmarkC"}, map[string]bool{"BenchmarkA": true})
	want := []string{"BenchmarkA", "BenchmarkC"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("required benchmarks = %v, want %v", got, want)
	}
}

func TestRequireBenchmarkGroupsRejectsMissingResult(t *testing.T) {
	groups := map[string][]*benchfmt.Result{
		"BenchmarkA": {{Name: benchfmt.Name("BenchmarkA")}},
	}
	if err := requireBenchmarkGroups([]string{"BenchmarkA", "BenchmarkB"}, groups); err == nil {
		t.Fatal("missing requested benchmark accepted")
	}
}

func TestMatchingBenchmarksAppliesRunSelection(t *testing.T) {
	got, err := matchingBenchmarks([]string{"BenchmarkA", "BenchmarkB"}, "^BenchmarkA$/^case$")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "BenchmarkA" {
		t.Fatalf("matching benchmarks = %v, want [BenchmarkA]", got)
	}
	if _, err := matchingBenchmarks([]string{"BenchmarkA"}, "["); err == nil {
		t.Fatal("invalid benchmark pattern accepted")
	}
	got, err = matchingBenchmarks([]string{"Benchmark_A"}, "Benchmark[ ]A")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "Benchmark_A" {
		t.Fatalf("rewritten-space match = %v, want [Benchmark_A]", got)
	}
}

func TestRestrictBenchmarkPatternPreservesSubBenchmarkSelection(t *testing.T) {
	got, err := restrictBenchmarkPattern("^BenchmarkA$/^one$|^BenchmarkB$/^two$", []string{"BenchmarkA"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "^(?:BenchmarkA)$/^one$" {
		t.Fatalf("restricted pattern = %q", got)
	}
}

func TestGitStateCacheSharesOnlyRecordedWritesAcrossModules(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "root.go"), []byte("package root\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	nestedSource := filepath.Join(nested, "nested.go")
	if err := os.WriteFile(nestedSource, []byte("package nested\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	raw, err := gogit.PlainInit(root, false)
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
	if _, err := wt.Commit("initial", &gogit.CommitOptions{Author: &object.Signature{Name: "t", Email: "t@example.invalid", When: time.Unix(1, 0)}}); err != nil {
		t.Fatal(err)
	}

	cache := newGitStateCache()
	rootState, err := cache.state(root)
	if err != nil {
		t.Fatal(err)
	}
	nestedState, err := cache.state(nested)
	if err != nil {
		t.Fatal(err)
	}
	recording := filepath.Join(root, "benchmarks", "BenchmarkRoot.txt")
	if err := os.Mkdir(filepath.Dir(recording), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(recording, []byte("result"), 0o644); err != nil {
		t.Fatal(err)
	}
	cache.recordWrites(rootState.Root(), []string{recording})
	current, err := gitblob.Snapshot(nested)
	if err != nil {
		t.Fatal(err)
	}
	if !nestedState.EqualExceptPaths(current, cache.writtenPaths(nestedState.Root())) {
		t.Fatal("earlier recording write tainted nested-module baseline")
	}
	if err := os.WriteFile(nestedSource, []byte("package nested\n// changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	current, err = gitblob.Snapshot(nested)
	if err != nil {
		t.Fatal(err)
	}
	if nestedState.EqualExceptPaths(current, cache.writtenPaths(nestedState.Root())) {
		t.Fatal("unrecorded source mutation was excluded")
	}
}

func TestRunRunKeepsSharedRepositoryModulesClean(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"go.mod":                "module example.com/root\n\ngo 1.25\n",
		"root_test.go":          "package root\n\nimport \"testing\"\n\nfunc BenchmarkRoot(b *testing.B) {}\n",
		"nested/go.mod":         "module example.com/nested\n\ngo 1.25\n",
		"nested/nested_test.go": "package nested\n\nimport \"testing\"\n\nfunc BenchmarkNested(b *testing.B) {}\n",
		"go.work":               "go 1.25\n\nuse (\n\t.\n\t./nested\n)\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := gogit.PlainInit(root, false)
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
	if _, err := wt.Commit("initial", &gogit.CommitOptions{Author: &object.Signature{Name: "t", Email: "t@example.invalid", When: time.Unix(1, 0)}}); err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)

	var out, errOut bytes.Buffer
	err = runRun(&out, &errOut, runConfig{
		benchDir: "results",
		opts:     runpkg.Options{Count: 1, Benchtime: "1x", Bench: "."},
	}, []string{".", "./nested"})
	if err != nil {
		t.Fatalf("runRun: %v\nstdout:\n%s\nstderr:\n%s", err, out.String(), errOut.String())
	}
	recs, err := store.New(filepath.Join(root, "results")).Read("", "BenchmarkNested", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := recs[0].GetConfig("dirty"); got != "false" {
		t.Fatalf("nested module dirty = %q, want false", got)
	}
}

func TestRunRecordsIncompleteRuntimeEvidence(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":        "module example.com/incompleterun\n\ngo 1.26.4\n",
		"bench_test.go": "package incompleterun\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc BenchmarkNoIO(b *testing.B) {}\nfunc BenchmarkReadError(b *testing.B) { _, _ = os.ReadFile(\"transiently-missing.txt\") }\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := gogit.PlainInit(dir, false)
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
	if _, err := wt.Commit("initial", &gogit.CommitOptions{Author: &object.Signature{Name: "t", Email: "t@example.invalid", When: time.Unix(1, 0)}}); err != nil {
		t.Fatal(err)
	}
	benchDir := filepath.Join(t.TempDir(), "benchmarks")
	withWorkingDir(t, dir)

	var out, errOut bytes.Buffer
	err = runRun(&out, &errOut, runConfig{
		benchDir: benchDir,
		opts:     runpkg.Options{Count: 1, Benchtime: "1x", Bench: "."},
	}, []string{"."})
	if err != nil {
		t.Fatalf("runRun: %v\nstdout:\n%s\nstderr:\n%s", err, out.String(), errOut.String())
	}
	e, err := newEngine(dir)
	if err != nil {
		t.Fatal(err)
	}
	st := store.New(benchDir)
	for _, bench := range []string{"BenchmarkNoIO", "BenchmarkReadError"} {
		recs, err := st.Read("", bench, "")
		if err != nil {
			t.Fatal(err)
		}
		fp, _, ok := fingerprintFromConfig(recs[0].Config)
		if !ok {
			t.Fatalf("%s recording lacks current format", bench)
		}
		if fp.RuntimeInputs == "" || fp.RuntimeDigest == "" {
			t.Fatalf("%s missing incomplete runtime evidence", bench)
		}
		assertRunConditionsLine(t, bench, recs[0].GetConfig("pew-runconditions"))
		// The recorded conditions are the *observed* ones, not a zero value.
		// The governor signal is boot-stable, so when this host can observe it
		// the recording must carry it; hosts without the signal leave this
		// unasserted (the unknown-marker rendering is pinned by unit tests).
		if fresh := runpkg.ObserveConditions(); fresh.Governor != "" {
			wantGovernor, _, _ := strings.Cut(strings.TrimPrefix(fresh.String(), "governor="), " ")
			if got, _, _ := strings.Cut(strings.TrimPrefix(recs[0].GetConfig("pew-runconditions"), "governor="), " "); got != wantGovernor {
				t.Errorf("%s recorded governor %q, want observed %q", bench, got, wantGovernor)
			}
		}
		v, reason, err := checkOne(st, e, "example.com/incompleterun", "", dir, bench, "")
		if err != nil {
			t.Fatal(err)
		}
		if v != verdictUnverifiable || reason != "testlog lacks operation outcome evidence" {
			t.Fatalf("%s recording = {%s %q}, want unverifiable incomplete observation", bench, v, reason)
		}
	}
}

// TestRunPackageRecordsProvidedConditions pins that runPackage records the
// observation handed to it verbatim (spec §9: the gate and the recording share
// one observation). It kills a records-zero-Conditions mutant deterministically
// on every host; a mutant that re-observes inside runPackage is caught only
// insofar as the synthetic values differ from the host's live signals (the
// governor equality check in TestRunRecordsIncompleteRuntimeEvidence adds the
// live-host layer where a governor signal exists).
func TestRunPackageRecordsProvidedConditions(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":        "module example.com/condwire\n\ngo 1.26.4\n",
		"bench_test.go": "package condwire\n\nimport \"testing\"\n\nfunc BenchmarkWire(b *testing.B) {}\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := gogit.PlainInit(dir, false)
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
	if _, err := wt.Commit("initial", &gogit.CommitOptions{Author: &object.Signature{Name: "t", Email: "t@example.invalid", When: time.Unix(1, 0)}}); err != nil {
		t.Fatal(err)
	}
	benchDir := filepath.Join(t.TempDir(), "benchmarks")
	withWorkingDir(t, dir)
	pkgs, err := resolvePackages([]string{"."})
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 1 {
		t.Fatalf("resolved %d packages, want 1", len(pkgs))
	}
	env := os.Environ()
	e, err := newEngineWithEnv(pkgs[0].Module.Dir, env)
	if err != nil {
		t.Fatal(err)
	}
	turbo, battery := true, false
	load := 1.25
	conditions := runpkg.Conditions{Governor: "performance", Turbo: &turbo, Load1: &load, Battery: &battery}
	var out bytes.Buffer
	// The recorded throttled field is the per-package measurement-bracket
	// delta (spec §9), not part of the provided pre-run observation: a stable
	// fake counter records an observed false.
	rc := runConfig{
		benchDir: benchDir,
		opts:     runpkg.Options{Count: 1, Benchtime: "1x", Bench: "."},
		throttle: func() runpkg.ThrottleSnapshot { return runpkg.ThrottleSnapshot{"c0": 5} },
	}
	if err := runPackage(&out, io.Discard, e, newGitStateCache(), rc, pkgs[0], env, conditions); err != nil {
		t.Fatalf("runPackage: %v\nstdout:\n%s", err, out.String())
	}
	recs, err := store.New(benchDir).Read("", "BenchmarkWire", "")
	if err != nil {
		t.Fatal(err)
	}
	want := "governor=performance turbo=on load1=1.25 throttled=false battery=false"
	if got := recs[0].GetConfig("pew-runconditions"); got != want {
		t.Fatalf("recorded conditions = %q, want the provided observation with the bracket delta %q", got, want)
	}
}

// TestRunPackageRecordsThrottleDelta pins spec §9's run-scoped throttling: a
// counter moving across the package's measurement bracket records
// throttled=true and warns after the measurement; under --strict the suspect
// measurement is refused with nothing recorded.
func TestRunPackageRecordsThrottleDelta(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":        "module example.com/throttlewire\n\ngo 1.26.4\n",
		"bench_test.go": "package throttlewire\n\nimport \"testing\"\n\nfunc BenchmarkHot(b *testing.B) {}\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := gogit.PlainInit(dir, false)
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
	if _, err := wt.Commit("initial", &gogit.CommitOptions{Author: &object.Signature{Name: "t", Email: "t@example.invalid", When: time.Unix(1, 0)}}); err != nil {
		t.Fatal(err)
	}
	withWorkingDir(t, dir)
	pkgs, err := resolvePackages([]string{"."})
	if err != nil {
		t.Fatal(err)
	}
	env := os.Environ()
	e, err := newEngineWithEnv(pkgs[0].Module.Dir, env)
	if err != nil {
		t.Fatal(err)
	}
	movingCounter := func() func() runpkg.ThrottleSnapshot {
		n := uint64(5)
		return func() runpkg.ThrottleSnapshot {
			n += 2
			return runpkg.ThrottleSnapshot{"c0": n}
		}
	}

	t.Run("records delta and warns", func(t *testing.T) {
		benchDir := filepath.Join(t.TempDir(), "benchmarks")
		var out, errOut bytes.Buffer
		rc := runConfig{benchDir: benchDir, opts: runpkg.Options{Count: 1, Benchtime: "1x", Bench: "."}, throttle: movingCounter()}
		if err := runPackage(&out, &errOut, e, newGitStateCache(), rc, pkgs[0], env, runpkg.Conditions{}); err != nil {
			t.Fatalf("runPackage: %v", err)
		}
		recs, err := store.New(benchDir).Read("", "BenchmarkHot", "")
		if err != nil {
			t.Fatal(err)
		}
		if got := recs[0].GetConfig("pew-runconditions"); !strings.Contains(got, "throttled=true") {
			t.Fatalf("recorded conditions = %q, want throttled=true from the bracket delta", got)
		}
		if !strings.Contains(errOut.String(), "thermal throttling occurred during example.com/throttlewire measurement") {
			t.Fatalf("stderr = %q, want the post-measurement throttling warning", errOut.String())
		}
	})

	t.Run("strict refuses the measurement", func(t *testing.T) {
		benchDir := filepath.Join(t.TempDir(), "benchmarks")
		// The refused benchmark's prior recording must survive untouched
		// (spec §9).
		st := store.New(benchDir)
		prior := []byte("goos: linux\nBenchmarkHot-8 1 99 ns/op\n")
		priorPath, err := st.Path("", "BenchmarkHot", "")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(priorPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(priorPath, prior, 0o644); err != nil {
			t.Fatal(err)
		}
		var out, errOut bytes.Buffer
		rc := runConfig{benchDir: benchDir, strict: true, opts: runpkg.Options{Count: 1, Benchtime: "1x", Bench: "."}, throttle: movingCounter()}
		err = runPackage(&out, &errOut, e, newGitStateCache(), rc, pkgs[0], env, runpkg.Conditions{})
		if err == nil || !strings.Contains(err.Error(), "thermal throttling during measurement") {
			t.Fatalf("err = %v, want the strict throttling refusal", err)
		}
		got, err := os.ReadFile(priorPath)
		if err != nil {
			t.Fatalf("prior recording gone: %v", err)
		}
		if !bytes.Equal(got, prior) {
			t.Fatalf("refused measurement modified the prior recording:\n%s", got)
		}
	})

	t.Run("build precedes the bracket", func(t *testing.T) {
		// The recorded verdict covers the measurement, not the build
		// (spec §9): the compile invocation must complete before the first
		// bracketing snapshot, and the measurement run sit strictly inside
		// the bracket.
		var events []string
		benchDir := filepath.Join(t.TempDir(), "benchmarks")
		rc := runConfig{
			benchDir: benchDir,
			opts:     runpkg.Options{Count: 1, Benchtime: "1x", Bench: "."},
			throttle: func() runpkg.ThrottleSnapshot {
				events = append(events, "snapshot")
				return runpkg.ThrottleSnapshot{"c0": 1}
			},
			execute: func(moduleDir, pin string, env, args []string) ([]byte, error) {
				for _, a := range args {
					if a == "-c" {
						events = append(events, "build")
						return nil, nil
					}
				}
				events = append(events, "measure")
				return []byte("goos: linux\ngoarch: amd64\npkg: example.com/throttlewire\ncpu: T\nBenchmarkHot-8 1 5 ns/op\nPASS\n"), nil
			},
		}
		if err := runPackage(io.Discard, io.Discard, e, newGitStateCache(), rc, pkgs[0], env, runpkg.Conditions{}); err != nil {
			t.Fatalf("runPackage: %v", err)
		}
		want := []string{"build", "snapshot", "measure", "snapshot"}
		if !reflect.DeepEqual(events, want) {
			t.Fatalf("invocation order = %v, want %v", events, want)
		}
	})
}

// assertRunConditionsLine checks a produced pew-runconditions value (spec §9):
// all five fields present in order, each either the explicit unknown marker or a
// plausibly observed value. On the Linux hosts that run this suite the real
// sysfs/procfs is observed, so this exercises genuine values; elsewhere every
// field is unknown.
func assertRunConditionsLine(t *testing.T, bench, value string) {
	t.Helper()
	if value == "" {
		t.Fatalf("%s recording missing pew-runconditions provenance", bench)
	}
	fields := strings.Fields(value)
	wantKeys := []string{"governor", "turbo", "load1", "throttled", "battery"}
	if len(fields) != len(wantKeys) {
		t.Fatalf("%s pew-runconditions = %q, want %d fields", bench, value, len(wantKeys))
	}
	for i, field := range fields {
		key, v, ok := strings.Cut(field, "=")
		if !ok || key != wantKeys[i] || v == "" {
			t.Fatalf("%s pew-runconditions field %d = %q, want %s=<value>", bench, i, field, wantKeys[i])
		}
		if v == "unknown" {
			continue
		}
		switch key {
		case "turbo":
			if v != "on" && v != "off" {
				t.Errorf("%s turbo = %q, want on/off/unknown", bench, v)
			}
		case "throttled", "battery":
			if v != "true" && v != "false" {
				t.Errorf("%s %s = %q, want true/false/unknown", bench, key, v)
			}
		case "load1":
			if _, err := strconv.ParseFloat(v, 64); err != nil {
				t.Errorf("%s load1 = %q, want a decimal", bench, v)
			}
		}
	}
}

// TestRunPackageSalvagesCorruptStream drives the real corruption mechanism end
// to end (spec §9 sample floor): a benchmark whose body writes a log line to
// stdout splices it into the framework's un-newlined name print, corrupting
// every one of its result lines. The corrupted benchmark must be refused with
// the offending lines surfaced, while the package's clean benchmark records
// normally — and its recording carries no salvage artifacts.
func TestRunPackageSalvagesCorruptStream(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/corruptstream\n\ngo 1.26.4\n",
		// The framework prints the first count-run's name and result together
		// after it completes, so BenchmarkNoisy's first row survives intact and
		// every later run is spliced: the refused benchmark holds a *partial*
		// valid sample set, pinning that partial data is dropped, not recorded.
		"bench_test.go": "package corruptstream\n\nimport (\n\t\"fmt\"\n\t\"testing\"\n)\n\n" +
			"func BenchmarkClean(b *testing.B) {}\n" +
			"func BenchmarkNoisy(b *testing.B) { fmt.Println(\"boot: node up, insecure transport\") }\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := gogit.PlainInit(dir, false)
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
	if _, err := wt.Commit("initial", &gogit.CommitOptions{Author: &object.Signature{Name: "t", Email: "t@example.invalid", When: time.Unix(1, 0)}}); err != nil {
		t.Fatal(err)
	}
	benchDir := filepath.Join(t.TempDir(), "benchmarks")
	// A refused benchmark's prior recording must survive untouched (spec §9).
	priorRecording := []byte("goos: linux\nBenchmarkNoisy-8 1 99 ns/op\n")
	if err := os.MkdirAll(benchDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(benchDir, "BenchmarkNoisy.txt"), priorRecording, 0o644); err != nil {
		t.Fatal(err)
	}
	withWorkingDir(t, dir)
	pkgs, err := resolvePackages([]string{"."})
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 1 {
		t.Fatalf("resolved %d packages, want 1", len(pkgs))
	}
	env := os.Environ()
	e, err := newEngineWithEnv(pkgs[0].Module.Dir, env)
	if err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	rc := runConfig{benchDir: benchDir, opts: runpkg.Options{Count: 2, Benchtime: "1x", Bench: "."}}
	refusal := runPackage(&out, &errOut, e, newGitStateCache(), rc, pkgs[0], env, runpkg.Conditions{})
	if refusal == nil {
		t.Fatalf("corrupted stream reported no error\nstdout:\n%s\nstderr:\n%s", out.String(), errOut.String())
	}
	if !strings.Contains(refusal.Error(), "BenchmarkNoisy") || !strings.Contains(refusal.Error(), "boot: node up") {
		t.Errorf("refusal error = %q, want the corrupted benchmark and the offending line", refusal)
	}
	if strings.Contains(refusal.Error(), "BenchmarkClean") {
		t.Errorf("refusal error = %q, must not implicate the clean benchmark", refusal)
	}
	if !strings.Contains(out.String(), "recorded     example.com/corruptstream.BenchmarkClean") {
		t.Errorf("clean benchmark not recorded; stdout:\n%s", out.String())
	}
	if !strings.Contains(errOut.String(), "corrupt benchmark output line") {
		t.Errorf("corrupt lines not surfaced on stderr:\n%s", errOut.String())
	}

	st := store.New(benchDir)
	recs, err := st.Read("", "BenchmarkClean", "")
	if err != nil {
		t.Fatalf("clean recording missing: %v", err)
	}
	if len(recs) != 2 {
		t.Fatalf("clean recording has %d samples, want the demanded 2", len(recs))
	}
	for _, rec := range recs {
		for _, cfg := range rec.Config {
			if cfg.Key == "boot" || strings.Contains(cfg.Key, "corrupt") || strings.Contains(string(cfg.Value), "node up") {
				t.Errorf("salvage artifact in recording config: %s: %s", cfg.Key, cfg.Value)
			}
		}
	}
	// BenchmarkNoisy still parsed one valid row (its first count-run); recording
	// that partial set would silently downgrade the demanded sample count.
	if !strings.Contains(refusal.Error(), "1 of 2 samples") {
		t.Errorf("refusal error = %q, want the 1-of-2 sample deficit named", refusal)
	}
	// Not recorded, and the prior recording survives byte-identical.
	got, err := os.ReadFile(filepath.Join(benchDir, "BenchmarkNoisy.txt"))
	if err != nil {
		t.Fatalf("prior recording gone: %v", err)
	}
	if !bytes.Equal(got, priorRecording) {
		t.Fatalf("refused benchmark's prior recording modified:\n%s", got)
	}
}

// TestRunPackageDropsForeignStreamConfig drives spec §5's closed recording key
// set (INV-12) end to end: a package whose init logs a `key: value`-shaped
// line before the benchmark header emits a standalone stream line that the
// benchmark-format reader takes as file configuration — no corruption, so
// nothing else refuses it. The run must record the benchmark without the
// foreign key and name the dropped key on stderr.
func TestRunPackageDropsForeignStreamConfig(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/foreignconfig\n\ngo 1.26.4\n",
		"bench_test.go": "package foreignconfig\n\nimport (\n\t\"fmt\"\n\t\"testing\"\n)\n\n" +
			"func init() { fmt.Println(\"raft: appending entries\") }\n" +
			"func BenchmarkClean(b *testing.B) {}\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := gogit.PlainInit(dir, false)
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
	if _, err := wt.Commit("initial", &gogit.CommitOptions{Author: &object.Signature{Name: "t", Email: "t@example.invalid", When: time.Unix(1, 0)}}); err != nil {
		t.Fatal(err)
	}
	benchDir := filepath.Join(t.TempDir(), "benchmarks")
	withWorkingDir(t, dir)
	pkgs, err := resolvePackages([]string{"."})
	if err != nil {
		t.Fatal(err)
	}
	env := os.Environ()
	e, err := newEngineWithEnv(pkgs[0].Module.Dir, env)
	if err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	rc := runConfig{benchDir: benchDir, opts: runpkg.Options{Count: 2, Benchtime: "1x", Bench: "."}}
	if err := runPackage(&out, &errOut, e, newGitStateCache(), rc, pkgs[0], env, runpkg.Conditions{}); err != nil {
		t.Fatalf("runPackage: %v\nstderr:\n%s", err, errOut.String())
	}
	if !strings.Contains(errOut.String(), `dropping stream configuration key "raft"`) {
		t.Errorf("dropped key not named on stderr:\n%s", errOut.String())
	}
	st := store.New(benchDir)
	recs, err := st.Read("", "BenchmarkClean", "")
	if err != nil {
		t.Fatalf("recording missing: %v", err)
	}
	// The recording's keys must be drawn only from spec §5's closed set
	// (INV-12) — this reads back what the real run path composed and wrote,
	// so a provenance key added anywhere along it surfaces here.
	closed := map[string]bool{
		"goos": true, "goarch": true, "pkg": true, "cpu": true,
		"pew-format": true, "commit": true, "toolchain": true, "machine": true,
		"buildconfig": true, "runtimeconfig": true, "dirty": true,
		"pew-runconditions": true, "pew-runtime": true, "pew-runtime-inputs": true,
		"pew-closure": true, "pew-purity": true, "pure": true,
	}
	for _, rec := range recs {
		for _, cfg := range rec.Config {
			if !closed[cfg.Key] {
				t.Errorf("recorded key %q outside spec §5's closed set (value %q)", cfg.Key, cfg.Value)
			}
		}
	}
}

// TestRunPackageRefusesUnattributableOrphan drives the package-refusal arm of
// the spec §9 sample floor end to end: a detached-measurement-fields line with
// no preceding benchmark name (foreign output printed before the first result)
// means a sample was destroyed or replaced somewhere pew cannot localize, so
// nothing at all may be recorded.
func TestRunPackageRefusesUnattributableOrphan(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/orphantail\n\ngo 1.26.4\n",
		// The framework flushes a benchmark body's first-run output before the
		// stream's first result line, so BenchmarkAAATail's fake tail precedes
		// every "Benchmark..." line: an unattributable orphan.
		"bench_test.go": "package orphantail\n\nimport (\n\t\"fmt\"\n\t\"testing\"\n)\n\n" +
			"func BenchmarkAAATail(b *testing.B) { fmt.Println(\"5 6 ns/op\") }\n" +
			"func BenchmarkClean(b *testing.B) {}\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := gogit.PlainInit(dir, false)
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
	if _, err := wt.Commit("initial", &gogit.CommitOptions{Author: &object.Signature{Name: "t", Email: "t@example.invalid", When: time.Unix(1, 0)}}); err != nil {
		t.Fatal(err)
	}
	benchDir := filepath.Join(t.TempDir(), "benchmarks")
	withWorkingDir(t, dir)
	pkgs, err := resolvePackages([]string{"."})
	if err != nil {
		t.Fatal(err)
	}
	if len(pkgs) != 1 {
		t.Fatalf("resolved %d packages, want 1", len(pkgs))
	}
	env := os.Environ()
	e, err := newEngineWithEnv(pkgs[0].Module.Dir, env)
	if err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	rc := runConfig{benchDir: benchDir, opts: runpkg.Options{Count: 1, Benchtime: "1x", Bench: "."}}
	refusal := runPackage(&out, &errOut, e, newGitStateCache(), rc, pkgs[0], env, runpkg.Conditions{})
	if refusal == nil {
		t.Fatalf("unattributable orphan reported no error\nstdout:\n%s\nstderr:\n%s", out.String(), errOut.String())
	}
	if !strings.Contains(refusal.Error(), "not attributable") {
		t.Errorf("refusal error = %q, want the unattributable-orphan cause", refusal)
	}
	if strings.Contains(out.String(), "recorded") {
		t.Errorf("package-refused run recorded something:\n%s", out.String())
	}
	if entries, err := os.ReadDir(benchDir); err == nil && len(entries) > 0 {
		t.Errorf("package-refused run left recordings: %v", entries)
	}
}

func TestRunStaleIntersectsBenchmarkPattern(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":        "module example.com/stalefilter\n\ngo 1.26.4\n",
		"bench_test.go": "package stalefilter\n\nimport \"testing\"\n\nfunc BenchmarkSelected(b *testing.B) {}\nfunc BenchmarkExcluded(b *testing.B) {}\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := gogit.PlainInit(dir, false)
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
	if _, err := wt.Commit("initial", &gogit.CommitOptions{Author: &object.Signature{Name: "t", Email: "t@example.invalid", When: time.Unix(1, 0)}}); err != nil {
		t.Fatal(err)
	}
	benchDir := filepath.Join(t.TempDir(), "benchmarks")
	withWorkingDir(t, dir)
	var out, errOut bytes.Buffer
	err = runRun(&out, &errOut, runConfig{
		benchDir:  benchDir,
		staleOnly: true,
		opts:      runpkg.Options{Count: 1, Benchtime: "1x", Bench: "^BenchmarkSelected$"},
	}, []string{"."})
	if err != nil {
		t.Fatalf("runRun: %v\nstdout:\n%s\nstderr:\n%s", err, out.String(), errOut.String())
	}
	st := store.New(benchDir)
	if _, err := st.Read("", "BenchmarkSelected", ""); err != nil {
		t.Fatalf("selected benchmark not recorded: %v", err)
	}
	if _, err := st.Read("", "BenchmarkExcluded", ""); !errors.Is(err, store.ErrNotRecorded) {
		t.Fatalf("excluded benchmark read error = %v, want not recorded", err)
	}
}

func TestSourceInputsDirtyIncludesIgnoredAndMetadataStableSource(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		".gitignore":     "generated.go\n",
		"go.mod":         "module example.com/buildinputs\n\ngo 1.25\n",
		"source.go":      "package buildinputs\n\nvar Value = 1\n",
		"source_test.go": "package buildinputs\n\nimport \"testing\"\n\nfunc BenchmarkValue(b *testing.B) { _ = Value }\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := gogit.PlainInit(dir, false)
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
	commit, err := wt.Commit("initial", &gogit.CommitOptions{Author: &object.Signature{Name: "t", Email: "t@example.invalid", When: time.Unix(1, 0)}})
	if err != nil {
		t.Fatal(err)
	}
	engine, err := gofresh.New(gofresh.WithDir(dir))
	if err != nil {
		t.Fatal(err)
	}
	view, err := engine.NewView(t.Context(), []gofresh.Subject{{Package: "example.com/buildinputs", Symbol: "BenchmarkValue"}}, dir)
	if err != nil {
		t.Fatal(err)
	}
	if dirty, err := sourceInputsDirty(dir, commit.String(), view.SourceFiles()); err != nil || dirty {
		t.Fatalf("committed build inputs: dirty=%v err=%v", dirty, err)
	}

	source := filepath.Join(dir, "source.go")
	info, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("package buildinputs\n\nvar Value = 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(source, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if dirty, err := sourceInputsDirty(dir, commit.String(), view.SourceFiles()); err != nil || !dirty {
		t.Fatalf("metadata-stable source change: dirty=%v err=%v", dirty, err)
	}
	if err := os.WriteFile(source, []byte(files["source.go"]), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "generated.go"), []byte("package buildinputs\n\nfunc init() { Value = 2 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	view, err = engine.NewView(t.Context(), []gofresh.Subject{{Package: "example.com/buildinputs", Symbol: "BenchmarkValue"}}, dir)
	if err != nil {
		t.Fatal(err)
	}
	if dirty, err := sourceInputsDirty(dir, commit.String(), view.SourceFiles()); err != nil || !dirty {
		t.Fatalf("ignored selected source: dirty=%v err=%v", dirty, err)
	}
}

func TestRejectRecordingDestinations(t *testing.T) {
	moduleDir := t.TempDir()
	benchDir := filepath.Join(moduleDir, "benchmarks")
	if err := os.Mkdir(benchDir, 0o755); err != nil {
		t.Fatal(err)
	}
	recording := filepath.Join(benchDir, "BenchmarkX.txt")
	if err := os.WriteFile(recording, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := rejectRecordingDestinations([]string{recording}, []string{recording}); err == nil {
		t.Fatal("recording destination equal to source input accepted")
	}
	aliasRoot := filepath.Join(t.TempDir(), "module-link")
	if err := os.Symlink(moduleDir, aliasRoot); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	aliasRecording := filepath.Join(aliasRoot, "benchmarks", "BenchmarkX.txt")
	if err := rejectRecordingDestinations([]string{recording}, []string{aliasRecording}); err == nil {
		t.Fatal("recording destination alias of source input accepted")
	}
	if err := rejectRecordingDestinations([]string{recording}, []string{filepath.Join(benchDir, "BenchmarkY.txt")}); err != nil {
		t.Fatalf("disjoint recording destination rejected: %v", err)
	}
}

func TestRunRunReturnsErrorOnPackageFailure(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/runfail\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "p_test.go"), []byte("package runfail\n\nimport \"testing\"\n\nfunc BenchmarkX(b *testing.B) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	var out, errOut bytes.Buffer
	err := runRun(&out, &errOut, runConfig{opts: runpkg.Options{Count: 1, Benchtime: "1x", Bench: "."}}, []string{"./..."})
	if err == nil {
		t.Fatal("runRun succeeded despite a per-package failure")
	}
	if !strings.Contains(out.String(), "error") {
		t.Fatalf("output = %q, want package error row", out.String())
	}
}
