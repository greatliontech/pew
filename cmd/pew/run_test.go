package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"runtime/pprof"
	"strconv"
	"strings"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	gofresh "github.com/greatliontech/gofresh"
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

func TestGitStateCacheExcludesRecordingStoresAcrossModules(t *testing.T) {
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

	// The invocation-wide union of recording stores is excluded from the
	// pinned repository state (spec §5): a sibling module's recordings
	// never taint this module's baseline, while source mutation anywhere
	// stays visible.
	cache := newGitStateCache([]string{filepath.Join(root, "benchmarks"), filepath.Join(nested, "benchmarks")})
	if _, err := cache.state(root); err != nil {
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
	current, err := cache.snapshot(nested)
	if err != nil {
		t.Fatal(err)
	}
	if !nestedState.Equal(current) {
		t.Fatal("earlier recording write tainted nested-module baseline")
	}
	if err := os.WriteFile(nestedSource, []byte("package nested\n// changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	current, err = cache.snapshot(nested)
	if err != nil {
		t.Fatal(err)
	}
	if nestedState.Equal(current) {
		t.Fatal("unrecorded source mutation was excluded")
	}
}

func TestRunRunKeepsSharedRepositoryModulesClean(t *testing.T) {
	// The test writes its own go.work; the ambient GOWORK must not
	// redirect module resolution (and a workspace-off oracle — gomutant's
	// ephemeral runs with GOWORK=off — must see the same tree).
	t.Setenv("GOWORK", "")

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

func TestRunRecordsCompletedRuntimeEvidence(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":                "module example.com/incompleterun\n\ngo 1.26.4\n",
		"pure/bench_test.go":    "package pure\n\nimport \"testing\"\n\nfunc BenchmarkNoIO(b *testing.B) {}\n",
		"readerr/bench_test.go": "package readerr\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc BenchmarkReadError(b *testing.B) { _, _ = os.ReadFile(\"transiently-missing.txt\") }\n",
		"getwd/bench_test.go":   "package getwd\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc BenchmarkGetwd(b *testing.B) { _ = os.Getenv(\"PWD\") }\n",
	}
	for name, content := range files {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
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
	}, []string{"./..."})
	if err != nil {
		t.Fatalf("runRun: %v\nstdout:\n%s\nstderr:\n%s", err, out.String(), errOut.String())
	}
	e, _, err := newEngineAt(dir, dir, false, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	st := store.New(benchDir)
	for bench, pkgRel := range map[string]string{"BenchmarkNoIO": "pure", "BenchmarkReadError": "readerr", "BenchmarkGetwd": "getwd"} {
		recs, err := st.Read(pkgRel, bench, "")
		if err != nil {
			t.Fatal(err)
		}
		fp, _, _, ok := fingerprintFromConfig(recs[0].Config)
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
		v, reason, _, _, err := checkOne(st, e, "example.com/incompleterun/"+pkgRel, pkgRel, dir, bench, "")
		if err != nil {
			t.Fatal(err)
		}
		// The completed conjunction replaces the blanket incompleteness
		// (spec §7.8): a clean-closure benchmark's recording verifies —
		// the runtime guard finally earns — while a file-reading one
		// refuses on its own closure, never on manufactured
		// incompleteness.
		switch bench {
		case "BenchmarkNoIO":
			if v != verdictValid {
				t.Fatalf("%s recording = {%s %q}, want valid under the completed observation", bench, v, reason)
			}
		case "BenchmarkGetwd":
			// Spawn and ingestion share one environment with PWD pinned to
			// the package directory the go driver gives the binary. The
			// verdict still refuses on the closure (pew selects no
			// observability proof), but the truthful pinned PWD is
			// admitted recordless — the manifest must never carry the
			// process-local-divergence seal an ingest under pew's own
			// environment would record.
			if v != verdictUnverifiable || !strings.Contains(reason, "os.Getenv") {
				t.Fatalf("%s recording = {%s %q}, want unverifiable on the closure reason", bench, v, reason)
			}
			manifest, decErr := base64.RawURLEncoding.DecodeString(fp.RuntimeInputs)
			if decErr != nil {
				t.Fatal(decErr)
			}
			if strings.Contains(string(manifest), "process-local environment input") {
				t.Fatalf("%s manifest = %s, want the PWD read observed rather than sealed process-local", bench, manifest)
			}
		case "BenchmarkReadError":
			if v != verdictUnverifiable || reason == "testlog lacks operation outcome evidence" || !strings.Contains(reason, "os.ReadFile") {
				t.Fatalf("%s recording = {%s %q}, want unverifiable on its own closure reason", bench, v, reason)
			}
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
	e, _, err := newEngineForPkg(pkgs[0], env)
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
	if err := runPackage(&out, io.Discard, e, newGitStateCache(nil), rc, pkgs[0], env, conditions, ""); err != nil {
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
	e, _, err := newEngineForPkg(pkgs[0], env)
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
		if err := runPackage(&out, &errOut, e, newGitStateCache(nil), rc, pkgs[0], env, runpkg.Conditions{}, ""); err != nil {
			t.Fatalf("runPackage: %v", err)
		}
		recs, err := store.New(benchDir).Read("", "BenchmarkHot", "")
		if err != nil {
			t.Fatal(err)
		}
		if got := recs[0].GetConfig("pew-runconditions"); !strings.Contains(got, "throttled=true") {
			t.Fatalf("recorded conditions = %q, want throttled=true from the bracket delta", got)
		}
		if !strings.Contains(errOut.String(), "thermal throttling occurred during example.com/throttlewire.BenchmarkHot measurement") {
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
		err = runPackage(&out, &errOut, e, newGitStateCache(nil), rc, pkgs[0], env, runpkg.Conditions{}, "")
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
		if err := runPackage(io.Discard, io.Discard, e, newGitStateCache(nil), rc, pkgs[0], env, runpkg.Conditions{}, ""); err != nil {
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

// TestRunPackagePGOContentMovesBuildconfig pins spec §5/§9's PGO content
// guarding end to end: two runs under the same GOFLAGS -pgo flag string but
// different profile bytes record different buildconfig digests — the guard
// covers the content the compile consumed, not the flag text. An unreadable
// named profile fails engine construction closed.
func TestRunPackagePGOContentMovesBuildconfig(t *testing.T) {
	var probeEnv []string
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GOFLAGS=") {
			probeEnv = append(probeEnv, entry)
		}
	}
	probeEnv = append(probeEnv, "GOFLAGS=-pgo=missing.pgo")
	if _, _, err := newEngineAt(t.TempDir(), t.TempDir(), false, probeEnv); err == nil {
		t.Fatal("unreadable named profile did not fail closed")
	}

	dir := t.TempDir()
	files := map[string]string{
		"go.mod":        "module example.com/pgowire\n\ngo 1.26.4\n",
		"bench_test.go": "package pgowire\n\nimport \"testing\"\n\nfunc BenchmarkPGO(b *testing.B) {}\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	profA, profB := generateCPUProfile(t), generateCPUProfile(t)
	if bytes.Equal(profA, profB) {
		t.Skip("two generated CPU profiles were byte-identical")
	}
	profPath := filepath.Join(dir, "prof.pgo")
	if err := os.WriteFile(profPath, profA, 0o644); err != nil {
		t.Fatal(err)
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
	var env []string
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GOFLAGS=") {
			env = append(env, entry)
		}
	}
	env = append(env, "GOFLAGS=-pgo=prof.pgo")

	record := func(benchDir string) string {
		t.Helper()
		e, pgoInput, err := newEngineForPkg(pkgs[0], env)
		if err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		rc := runConfig{benchDir: benchDir, opts: runpkg.Options{Count: 1, Benchtime: "1x", Bench: "."}}
		if err := runPackage(&out, io.Discard, e, newGitStateCache(nil), rc, pkgs[0], env, runpkg.Conditions{}, pgoInput); err != nil {
			t.Fatalf("runPackage: %v\nstdout:\n%s", err, out.String())
		}
		recs, err := store.New(benchDir).Read("", "BenchmarkPGO", "")
		if err != nil {
			t.Fatal(err)
		}
		bc := recs[0].GetConfig("buildconfig")
		if bc == "" {
			t.Fatal("recording missing buildconfig")
		}
		return bc
	}

	bcA := record(filepath.Join(t.TempDir(), "benchmarks"))
	bcARepeat := record(filepath.Join(t.TempDir(), "benchmarks"))
	if bcA != bcARepeat {
		t.Fatalf("same profile bytes moved buildconfig: %q vs %q", bcA, bcARepeat)
	}
	if err := os.WriteFile(profPath, profB, 0o644); err != nil {
		t.Fatal(err)
	}
	if bcB := record(filepath.Join(t.TempDir(), "benchmarks")); bcB == bcA {
		t.Fatalf("profile content change left buildconfig unmoved: %q", bcB)
	}
}

// generateCPUProfile produces a small valid pprof profile — PGO tests need
// bytes the compile genuinely consumes, so a garbage stand-in cannot serve.
func generateCPUProfile(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := pprof.StartCPUProfile(&buf); err != nil {
		t.Fatal(err)
	}
	// The busy work hashes PRIVATE data: the profile buffer is being
	// written concurrently by pprof's builder goroutine, so reading it
	// mid-profile is a data race (it fired under -race on three PGO
	// tests before this seed replaced buf.Bytes()).
	seed := [32]byte{1}
	deadline := time.Now().Add(20 * time.Millisecond)
	for time.Now().Before(deadline) {
		seed = sha256.Sum256(seed[:])
	}
	pprof.StopCPUProfile()
	_ = seed
	return buf.Bytes()
}

// TestRunPackageDefaultPGOMovesBuildconfig pins the second §9 PGO channel: a
// tested main package's default.pgo is consumed under the default -pgo=auto,
// so its content digest must ride the recorded buildconfig — regenerating the
// profile moves the guard with no flag change anywhere.
func TestRunPackageDefaultPGOMovesBuildconfig(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":        "module example.com/pgomain\n\ngo 1.26.4\n",
		"main.go":       "package main\n\nfunc main() {}\n",
		"bench_test.go": "package main\n\nimport \"testing\"\n\nfunc BenchmarkMain(b *testing.B) {}\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	profA, profB := generateCPUProfile(t), generateCPUProfile(t)
	if bytes.Equal(profA, profB) {
		t.Skip("two generated CPU profiles were byte-identical")
	}
	profPath := filepath.Join(dir, "default.pgo")
	if err := os.WriteFile(profPath, profA, 0o644); err != nil {
		t.Fatal(err)
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
	if pkgs[0].Name != "main" {
		t.Fatalf("resolved package name %q, want main", pkgs[0].Name)
	}
	var env []string
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GOFLAGS=") {
			env = append(env, entry)
		}
	}

	record := func(benchDir string) string {
		t.Helper()
		e, pgoInput, err := newEngineForPkg(pkgs[0], env)
		if err != nil {
			t.Fatal(err)
		}
		if pgoInput == "" {
			t.Fatal("tested main package's default.pgo yielded no build input")
		}
		var out bytes.Buffer
		rc := runConfig{benchDir: benchDir, opts: runpkg.Options{Count: 1, Benchtime: "1x", Bench: "."}}
		if err := runPackage(&out, io.Discard, e, newGitStateCache(nil), rc, pkgs[0], env, runpkg.Conditions{}, pgoInput); err != nil {
			t.Fatalf("runPackage: %v\nstdout:\n%s", err, out.String())
		}
		recs, err := store.New(benchDir).Read("", "BenchmarkMain", "")
		if err != nil {
			t.Fatal(err)
		}
		return recs[0].GetConfig("buildconfig")
	}

	bcA := record(filepath.Join(t.TempDir(), "benchmarks"))
	if err := os.WriteFile(profPath, profB, 0o644); err != nil {
		t.Fatal(err)
	}
	// The profile is untracked-content-relevant but the tree state moved:
	// commit the new profile so the producer's repository-state checks pass.
	if err := wt.AddGlob("."); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Commit("profile-b", &gogit.CommitOptions{Author: &object.Signature{Name: "t", Email: "t@example.invalid", When: time.Unix(2, 0)}}); err != nil {
		t.Fatal(err)
	}
	if bcB := record(filepath.Join(t.TempDir(), "benchmarks")); bcB == bcA || bcB == "" {
		t.Fatalf("default.pgo content change left buildconfig unmoved: %q", bcB)
	}
}

// TestRunPackageRefusesPGOProfileDrift pins the §9 producer revalidation: a
// profile edited between guard capture and the recording write makes the
// recorded digest describe bytes the compile never consumed, so the package
// is refused and nothing is recorded.
func TestRunPackageRefusesPGOProfileDrift(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":        "module example.com/pgodrift\n\ngo 1.26.4\n",
		"bench_test.go": "package pgodrift\n\nimport \"testing\"\n\nfunc BenchmarkDrift(b *testing.B) {}\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	profA, profB := generateCPUProfile(t), generateCPUProfile(t)
	if bytes.Equal(profA, profB) {
		t.Skip("two generated CPU profiles were byte-identical")
	}
	profPath := filepath.Join(dir, "prof.pgo")
	if err := os.WriteFile(profPath, profA, 0o644); err != nil {
		t.Fatal(err)
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
	var env []string
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GOFLAGS=") {
			env = append(env, entry)
		}
	}
	env = append(env, "GOFLAGS=-pgo=prof.pgo")
	e, pgoInput, err := newEngineForPkg(pkgs[0], env)
	if err != nil {
		t.Fatal(err)
	}
	// Drift lands after guard capture: the digest the engine folded into
	// buildconfig no longer matches the bytes the measured compile consumes.
	if err := os.WriteFile(profPath, profB, 0o644); err != nil {
		t.Fatal(err)
	}
	benchDir := filepath.Join(t.TempDir(), "benchmarks")
	var out bytes.Buffer
	rc := runConfig{benchDir: benchDir, opts: runpkg.Options{Count: 1, Benchtime: "1x", Bench: "."}}
	err = runPackage(&out, io.Discard, e, newGitStateCache(nil), rc, pkgs[0], env, runpkg.Conditions{}, pgoInput)
	if err == nil || !strings.Contains(err.Error(), "effective PGO input changed") {
		t.Fatalf("err = %v, want the PGO drift refusal", err)
	}
	if _, err := store.New(benchDir).Read("", "BenchmarkDrift", ""); err == nil {
		t.Fatal("drifted-profile measurement was recorded")
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
	e, _, err := newEngineForPkg(pkgs[0], env)
	if err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	rc := runConfig{benchDir: benchDir, opts: runpkg.Options{Count: 2, Benchtime: "1x", Bench: "."}}
	refusal := runPackage(&out, &errOut, e, newGitStateCache(nil), rc, pkgs[0], env, runpkg.Conditions{}, "")
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
// set (REQ-pew-key-set) end to end: a package whose init logs a `key: value`-shaped
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
	e, _, err := newEngineForPkg(pkgs[0], env)
	if err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	rc := runConfig{benchDir: benchDir, opts: runpkg.Options{Count: 2, Benchtime: "1x", Bench: "."}}
	if err := runPackage(&out, &errOut, e, newGitStateCache(nil), rc, pkgs[0], env, runpkg.Conditions{}, ""); err != nil {
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
	// (REQ-pew-key-set) — this reads back what the real run path composed and wrote,
	// so a provenance key added anywhere along it surfaces here.
	closed := map[string]bool{"goos": true, "goarch": true, "pkg": true, "cpu": true}
	for _, k := range runpkg.RecordingConfigKeys {
		closed[k] = true
	}
	for _, rec := range recs {
		for _, cfg := range rec.Config {
			if !closed[cfg.Key] {
				t.Errorf("recorded key %q outside spec §5's closed set (value %q)", cfg.Key, cfg.Value)
			}
		}
	}
}

// TestRunPackageRefusesUnattributableOrphan drives the unattributable-orphan
// arm of the spec §9 sample floor end to end: a detached-measurement-fields
// line with no preceding benchmark name (foreign output printed before the
// first result) means a sample was destroyed or replaced somewhere the stream
// cannot localize. Under single-subject execution the suspect process ran
// exactly one benchmark, so the refusal is arm-scoped: the orphan-producing
// benchmark records nothing while its sibling — a separate process with a
// clean stream — records normally.
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
	e, _, err := newEngineForPkg(pkgs[0], env)
	if err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	rc := runConfig{benchDir: benchDir, opts: runpkg.Options{Count: 1, Benchtime: "1x", Bench: "."}}
	refusal := runPackage(&out, &errOut, e, newGitStateCache(nil), rc, pkgs[0], env, runpkg.Conditions{}, "")
	if refusal == nil {
		t.Fatalf("unattributable orphan reported no error\nstdout:\n%s\nstderr:\n%s", out.String(), errOut.String())
	}
	if !strings.Contains(refusal.Error(), "not attributable") || !strings.Contains(refusal.Error(), "BenchmarkAAATail") {
		t.Errorf("refusal error = %q, want the unattributable-orphan cause on BenchmarkAAATail", refusal)
	}
	st := store.New(benchDir)
	if _, err := st.Read("", "BenchmarkAAATail", ""); !errors.Is(err, store.ErrNotRecorded) {
		t.Errorf("orphan-tainted arm read error = %v, want not recorded", err)
	}
	if _, err := st.Read("", "BenchmarkClean", ""); err != nil {
		t.Errorf("sibling arm not recorded: %v", err)
	}
	if !strings.Contains(out.String(), "recorded     example.com/orphantail.BenchmarkClean") {
		t.Errorf("clean sibling arm missing from stdout:\n%s", out.String())
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

// TestRunObservesThroughSymlinkedModule pins spawn/ingest environment
// fidelity through a symlinked checkout under the truthful-PWD posture
// of a shell-launched run. Three assertions, three mutations: the seal
// assertion kills an ingest under pew's own unpinned environment; the
// module-relative assertion kills both the resolved-root-spawn revert
// (whose reads classify as alias abs-paths) and a conjunction that
// regressed to the fallback (whose manifest carries no path); the
// alias assertion independently kills the revert.
func TestRunObservesThroughSymlinkedModule(t *testing.T) {
	real := filepath.Join(t.TempDir(), "real")
	if err := os.MkdirAll(filepath.Join(real, "getwd"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"go.mod":                "module example.com/symlinkedrun\n\ngo 1.26.4\n",
		"getwd/bench_test.go":   "package getwd\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc BenchmarkPWDRead(b *testing.B) { _, _ = os.ReadFile(os.Getenv(\"PWD\") + \"/bench_input.txt\") }\n",
		"getwd/bench_input.txt": "input\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(real, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	raw, err := gogit.PlainInit(real, false)
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
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	benchDir := filepath.Join(t.TempDir(), "benchmarks")
	withWorkingDir(t, link)
	// The truthful-PWD posture of a shell-launched run: go's Getwd (and
	// go list's module dir) honor an exported PWD naming the alias, so
	// the module dir the run sees is the SYMLINK path — the shape the
	// resolved-root spawn exists for.
	t.Setenv("PWD", link)

	var out, errOut bytes.Buffer
	err = runRun(&out, &errOut, runConfig{
		benchDir: benchDir,
		opts:     runpkg.Options{Count: 1, Benchtime: "1x", Bench: "."},
	}, []string{"./..."})
	if err != nil {
		t.Fatalf("runRun: %v\nstdout:\n%s\nstderr:\n%s", err, out.String(), errOut.String())
	}
	st := store.New(benchDir)
	recs, err := st.Read("getwd", "BenchmarkPWDRead", "")
	if err != nil {
		t.Fatal(err)
	}
	fp, _, _, ok := fingerprintFromConfig(recs[0].Config)
	if !ok {
		t.Fatal("recording lacks current format")
	}
	manifest, err := base64.RawURLEncoding.DecodeString(fp.RuntimeInputs)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(manifest), "process-local environment input") {
		t.Fatalf("manifest = %s, want spawn-faithful PWD through the symlink rather than the process-local seal", manifest)
	}
	// The completed shape, positively: the $PWD-derived read normalizes
	// to a module-relative identity — proof the observation completed
	// and resolved the read inside the bracketed module (a
	// fallback-incomplete manifest carries no path at all), and no alias
	// path leaks into the durable evidence.
	if !strings.Contains(string(manifest), `"getwd/bench_input.txt"`) {
		t.Fatalf("manifest = %s, want the observed input recorded module-relative", manifest)
	}
	if strings.Contains(string(manifest), link) {
		t.Fatalf("manifest = %s, records the alias path %s", manifest, link)
	}
}

// The //pew:scratch directive rides the whole path — discovery in the
// package's test files, runPackage, ingest — so a bench's own
// created-and-removed scratch leaves no manifest identities, while the
// identical bench without the declaration records them (spec §7.8).
func TestRunScratchDirectiveKeepsManifestClean(t *testing.T) {
	dir := t.TempDir()
	benchBody := `package p

import (
	"os"
	"path/filepath"
	"testing"
)

func BenchmarkScratch(b *testing.B) {
	d, err := os.MkdirTemp(".", "sb-*")
	if err != nil {
		b.Fatal(err)
	}
	defer os.RemoveAll(d)
	p := filepath.Join(d, "out.txt")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		b.Fatal(err)
	}
	if _, err := os.ReadFile(p); err != nil {
		b.Fatal(err)
	}
}
`
	files := map[string]string{
		"go.mod":                   "module example.com/scratchrun\n\ngo 1.26.4\n",
		"declared/bench_test.go":   "//pew:scratch sb-*\n" + benchBody,
		"undeclared/bench_test.go": benchBody,
	}
	for name, content := range files {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
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

	// A killed run's leftover in the DECLARED namespace is swept before
	// the bracket forms (with a printed notice); the same name in the
	// undeclared package is ordinary state and survives untouched.
	for _, pkg := range []string{"declared", "undeclared"} {
		if err := os.MkdirAll(filepath.Join(dir, pkg, "sb-stale123"), 0o755); err != nil {
			t.Fatal(err)
		}
		// The leftover carries a git-VISIBLE file: a sweep after the
		// module state baseline pins would abort the whole run as
		// "repository state moved" — the sweep must precede the pin.
		if err := os.WriteFile(filepath.Join(dir, pkg, "sb-stale123", "out.txt"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	var out, errOut bytes.Buffer
	if err := runRun(&out, &errOut, runConfig{
		benchDir: benchDir,
		opts:     runpkg.Options{Count: 1, Benchtime: "1x", Bench: "."},
	}, []string{"./..."}); err != nil {
		t.Fatalf("runRun: %v\nstdout:\n%s\nstderr:\n%s", err, out.String(), errOut.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "declared", "sb-stale123")); !os.IsNotExist(err) {
		t.Fatal("declared-namespace leftover survived the pre-bracket sweep")
	}
	if !strings.Contains(errOut.String(), "swept stale run-scratch") || !strings.Contains(errOut.String(), "sb-stale123") {
		t.Fatalf("sweep notice missing:\n%s", errOut.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "undeclared", "sb-stale123")); err != nil {
		t.Fatalf("undeclared package's dir was swept without a directive: %v", err)
	}
	st := store.New(benchDir)
	for pkgRel, wantScratch := range map[string]bool{"declared": false, "undeclared": true} {
		recs, err := st.Read(pkgRel, "BenchmarkScratch", "")
		if err != nil {
			t.Fatal(err)
		}
		if len(recs) == 0 {
			t.Fatalf("%s: no recording", pkgRel)
		}
		fp, _, _, ok := fingerprintFromConfig(recs[0].Config)
		if !ok {
			t.Fatalf("%s recording lacks current format", pkgRel)
		}
		manifest, err := base64.RawURLEncoding.DecodeString(fp.RuntimeInputs)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.Contains(string(manifest), "sb-"); got != wantScratch {
			t.Fatalf("%s manifest scratch identities = %v, want %v; manifest: %s", pkgRel, got, wantScratch, manifest)
		}
	}
}

// moduleBenchDir resolves through symlinks even when the store does not
// exist yet — the nearest existing ancestor resolves and the tail rejoins —
// so a relative --bench-dir made absolute through an alias cwd still
// matches the resolved paths the exclusion compares against (spec §5).
func TestModuleBenchDirResolvesThroughMissingStore(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.Mkdir(real, 0o755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(real, alias); err != nil {
		t.Fatal(err)
	}
	resolvedReal, err := filepath.EvalSymlinks(real)
	if err != nil {
		t.Fatal(err)
	}
	got, err := moduleBenchDir("", alias)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(resolvedReal, "benchmarks"); got != want {
		t.Fatalf("moduleBenchDir(alias) = %q, want %q", got, want)
	}
}

// The exclusion's precondition is enforced, not assumed: measured source
// under the recording store refuses the run (spec §5).
func TestRejectStoreCoveredSources(t *testing.T) {
	base := t.TempDir()
	store := filepath.Join(base, "benchmarks")
	src := filepath.Join(store, "pkg", "code.go")
	if err := rejectStoreCoveredSources([]string{src}, store); err == nil {
		t.Fatal("measured source under the store accepted")
	}
	outside := filepath.Join(base, "pkg", "code.go")
	if err := rejectStoreCoveredSources([]string{outside}, store); err != nil {
		t.Fatalf("source outside the store refused: %v", err)
	}
}

// TestRunPerArmRuntimeManifestAttribution pins single-subject execution's
// evidence attribution (spec §9, §7.8): each benchmark measures in its own
// `go test` process with its own testlog capture, so a recording's
// runtime-input manifest carries exactly its own benchmark's reads. A
// shared-process model serves one union manifest to every sibling, so this
// fails there by construction.
func TestRunPerArmRuntimeManifestAttribution(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/armattrib\n\ngo 1.26.4\n",
		"bench_test.go": "package armattrib\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\n" +
			"func BenchmarkAlpha(b *testing.B) { _, _ = os.ReadFile(\"alpha_input.txt\") }\n" +
			"func BenchmarkBeta(b *testing.B) { _, _ = os.ReadFile(\"beta_input.txt\") }\n",
		"alpha_input.txt": "alpha\n",
		"beta_input.txt":  "beta\n",
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
	if err := runRun(&out, &errOut, runConfig{
		benchDir: benchDir,
		opts:     runpkg.Options{Count: 1, Benchtime: "1x", Bench: "."},
	}, []string{"."}); err != nil {
		t.Fatalf("runRun: %v\nstdout:\n%s\nstderr:\n%s", err, out.String(), errOut.String())
	}
	st := store.New(benchDir)
	for bench, want := range map[string]struct{ own, sibling string }{
		"BenchmarkAlpha": {own: "alpha_input.txt", sibling: "beta_input.txt"},
		"BenchmarkBeta":  {own: "beta_input.txt", sibling: "alpha_input.txt"},
	} {
		recs, err := st.Read("", bench, "")
		if err != nil {
			t.Fatal(err)
		}
		fp, _, _, ok := fingerprintFromConfig(recs[0].Config)
		if !ok {
			t.Fatalf("%s recording lacks current format", bench)
		}
		manifest, err := base64.RawURLEncoding.DecodeString(fp.RuntimeInputs)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(manifest), want.own) {
			t.Errorf("%s manifest lacks its own read %s: %s", bench, want.own, manifest)
		}
		if strings.Contains(string(manifest), want.sibling) {
			t.Errorf("%s manifest carries the sibling's read %s: %s", bench, want.sibling, manifest)
		}
	}
}

// TestRunSingleSubjectProcessIsolation pins that sibling benchmarks never
// share process state (spec §9): BenchmarkMutate increments a package-level
// counter; BenchmarkObserve reports the counter as a metric. In one shared
// process (source and sorted order both run Mutate first) the increments
// leak into the observed metric; in per-benchmark processes the counter is
// untouched at observation time, so the recorded metric is exactly zero.
func TestRunSingleSubjectProcessIsolation(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/procisolation\n\ngo 1.26.4\n",
		"bench_test.go": "package procisolation\n\nimport \"testing\"\n\nvar n int\n\n" +
			"func BenchmarkMutate(b *testing.B) {\n\tfor i := 0; i < b.N; i++ {\n\t\tn++\n\t}\n}\n\n" +
			"func BenchmarkObserve(b *testing.B) {\n\tb.ReportMetric(float64(n), \"leak\")\n}\n",
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
	if err := runRun(&out, &errOut, runConfig{
		benchDir: benchDir,
		opts:     runpkg.Options{Count: 1, Benchtime: "1x", Bench: "."},
	}, []string{"."}); err != nil {
		t.Fatalf("runRun: %v\nstdout:\n%s\nstderr:\n%s", err, out.String(), errOut.String())
	}
	recs, err := store.New(benchDir).Read("", "BenchmarkObserve", "")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, rec := range recs {
		for _, v := range rec.Values {
			if v.Unit == "leak" {
				found = true
				if v.Value != 0 {
					t.Fatalf("leak metric = %v, want 0: sibling process state leaked into the measurement", v.Value)
				}
			}
		}
	}
	if !found {
		t.Fatal("BenchmarkObserve recording carries no leak metric")
	}
}

// TestRunFailingArmDiscardsOnlyItsRecording pins spec §9's failure isolation
// under single-subject execution: a benchmark whose process fails (b.Fatal)
// records nothing, the sibling arm's process records a complete well-formed
// recording, and the command still reports the failure and exits non-zero.
func TestRunFailingArmDiscardsOnlyItsRecording(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/armfail\n\ngo 1.26.4\n",
		"bench_test.go": "package armfail\n\nimport \"testing\"\n\n" +
			"func BenchmarkGood(b *testing.B) {}\n" +
			"func BenchmarkBad(b *testing.B) { b.Fatal(\"boom\") }\n",
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
	if err == nil {
		t.Fatalf("runRun succeeded despite a failing arm\nstdout:\n%s", out.String())
	}
	if !strings.Contains(err.Error(), "example.com/armfail") {
		t.Errorf("err = %v, want the failing package named", err)
	}
	if !strings.Contains(out.String(), "BenchmarkBad") {
		t.Errorf("stdout does not name the failed arm:\n%s", out.String())
	}
	st := store.New(benchDir)
	if _, err := st.Read("", "BenchmarkBad", ""); !errors.Is(err, store.ErrNotRecorded) {
		t.Errorf("failing arm read error = %v, want not recorded", err)
	}
	recs, err := st.Read("", "BenchmarkGood", "")
	if err != nil {
		t.Fatalf("sibling arm not recorded: %v", err)
	}
	if _, _, _, ok := fingerprintFromConfig(recs[0].Config); !ok {
		t.Error("sibling arm's recording lacks the current well-formed format")
	}
	assertRunConditionsLine(t, "BenchmarkGood", recs[0].GetConfig("pew-runconditions"))
	if !strings.Contains(out.String(), "recorded     example.com/armfail.BenchmarkGood") {
		t.Errorf("sibling arm missing from stdout:\n%s", out.String())
	}
}

// TestRunPackagePerArmThrottleAttribution pins that the throttle bracket and
// its recorded verdict are per benchmark (spec §9): with counters that move
// only across the second arm's measurement, the first arm records
// throttled=false and the second throttled=true, the warning names the
// throttled benchmark, and the invocation order is one build followed by a
// snapshot/measure/snapshot bracket per arm.
func TestRunPackagePerArmThrottleAttribution(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/armthrottle\n\ngo 1.26.4\n",
		"bench_test.go": "package armthrottle\n\nimport \"testing\"\n\n" +
			"func BenchmarkFirst(b *testing.B) {}\n" +
			"func BenchmarkSecond(b *testing.B) {}\n",
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
	e, _, err := newEngineForPkg(pkgs[0], env)
	if err != nil {
		t.Fatal(err)
	}

	var events []string
	snapshots := 0
	rc := runConfig{
		benchDir: benchDir,
		opts:     runpkg.Options{Count: 1, Benchtime: "1x", Bench: "."},
		throttle: func() runpkg.ThrottleSnapshot {
			snapshots++
			events = append(events, "snapshot")
			// Snapshots 1+2 bracket the first arm (counter still); snapshots
			// 3+4 bracket the second (counter moves within the bracket).
			n := uint64(1)
			if snapshots >= 4 {
				n = 2
			}
			return runpkg.ThrottleSnapshot{"c0": n}
		},
		execute: func(moduleDir, pin string, env, args []string) ([]byte, error) {
			for _, a := range args {
				if a == "-c" {
					events = append(events, "build")
					return nil, nil
				}
			}
			pattern := ""
			for i, a := range args {
				if a == "-bench" && i+1 < len(args) {
					pattern = args[i+1]
				}
			}
			name := "First"
			if strings.Contains(pattern, "Second") {
				name = "Second"
			}
			events = append(events, "measure"+name)
			return []byte(fmt.Sprintf("goos: %s\ngoarch: %s\npkg: example.com/armthrottle\ncpu: T\nBenchmark%s-8 1 5 ns/op\nPASS\n", runtime.GOOS, runtime.GOARCH, name)), nil
		},
	}
	var out, errOut bytes.Buffer
	if err := runPackage(&out, &errOut, e, newGitStateCache(nil), rc, pkgs[0], env, runpkg.Conditions{}, ""); err != nil {
		t.Fatalf("runPackage: %v\nstderr:\n%s", err, errOut.String())
	}
	want := []string{"build", "snapshot", "measureFirst", "snapshot", "snapshot", "measureSecond", "snapshot"}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("invocation order = %v, want %v", events, want)
	}
	st := store.New(benchDir)
	for bench, wantThrottled := range map[string]string{"BenchmarkFirst": "throttled=false", "BenchmarkSecond": "throttled=true"} {
		recs, err := st.Read("", bench, "")
		if err != nil {
			t.Fatal(err)
		}
		if got := recs[0].GetConfig("pew-runconditions"); !strings.Contains(got, wantThrottled) {
			t.Errorf("%s conditions = %q, want %s from its own bracket delta", bench, got, wantThrottled)
		}
	}
	if !strings.Contains(errOut.String(), "thermal throttling occurred during example.com/armthrottle.BenchmarkSecond measurement") {
		t.Errorf("stderr = %q, want the throttled arm named", errOut.String())
	}
	if strings.Contains(errOut.String(), "BenchmarkFirst measurement") {
		t.Errorf("stderr wrongly implicates the quiet arm:\n%s", errOut.String())
	}
}

// TestRunFailingArmResidueRefusesOnlyItsArm pins the per-arm repository-state
// bracket (spec §9): a crashing benchmark that also leaves worktree residue
// breaks only its own arm's evidence premise. The sibling — whose own bracket
// opens after the residue exists and holds across its measurement — still
// records, and the package error names BOTH facts about the broken arm: the
// process failure and the moved state bracket, neither masking the other.
func TestRunFailingArmResidueRefusesOnlyItsArm(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/armresidue\n\ngo 1.26.4\n",
		"bench_test.go": "package armresidue\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\n" +
			"func BenchmarkBad(b *testing.B) {\n\tif err := os.WriteFile(\"crash-residue.txt\", []byte(\"x\"), 0o644); err != nil {\n\t\tb.Fatal(err)\n\t}\n\tb.Fatal(\"boom\")\n}\n\n" +
			"func BenchmarkGood(b *testing.B) {}\n",
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
	if err == nil {
		t.Fatalf("runRun succeeded despite a crashing, residue-leaving arm\nstdout:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "BenchmarkBad") {
		t.Errorf("stdout does not name the failed arm:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "repository state moved during BenchmarkBad measurement") {
		t.Errorf("stdout does not name the moved-state refusal for the writing arm:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "benchmark(s) failed") {
		t.Errorf("stdout does not report the process failure alongside the refusal:\n%s", out.String())
	}
	st := store.New(benchDir)
	if _, err := st.Read("", "BenchmarkBad", ""); !errors.Is(err, store.ErrNotRecorded) {
		t.Errorf("residue-leaving arm read error = %v, want not recorded", err)
	}
	recs, err := st.Read("", "BenchmarkGood", "")
	if err != nil {
		t.Fatalf("sibling arm not recorded despite a clean bracket of its own: %v", err)
	}
	if _, _, _, ok := fingerprintFromConfig(recs[0].Config); !ok {
		t.Error("sibling arm's recording lacks the current well-formed format")
	}
	if !strings.Contains(out.String(), "recorded     example.com/armresidue.BenchmarkGood") {
		t.Errorf("sibling arm missing from stdout:\n%s", out.String())
	}
}

// TestRunPackageRefusesForeignCorruptEvidence pins the single-subject audit's
// fail-closed handling of corruption attributed to a non-subject benchmark
// (spec §9): a single-subject stream cannot legitimately carry any sibling's
// name, so an unparseable line beginning with one is the same splice evidence
// as a parseable foreign row and refuses the arm — never a stderr warning
// that lets the arm record.
func TestRunPackageRefusesForeignCorruptEvidence(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/foreigncorrupt\n\ngo 1.26.4\n",
		"bench_test.go": "package foreigncorrupt\n\nimport \"testing\"\n\n" +
			"func BenchmarkOther(b *testing.B) {}\n" +
			"func BenchmarkSubject(b *testing.B) {}\n",
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
	e, _, err := newEngineForPkg(pkgs[0], env)
	if err != nil {
		t.Fatal(err)
	}
	rc := runConfig{
		benchDir: benchDir,
		opts:     runpkg.Options{Count: 1, Benchtime: "1x", Bench: "."},
		execute: func(moduleDir, pin string, env, args []string) ([]byte, error) {
			for _, a := range args {
				if a == "-c" {
					return nil, nil
				}
			}
			pattern := ""
			for i, a := range args {
				if a == "-bench" && i+1 < len(args) {
					pattern = args[i+1]
				}
			}
			header := fmt.Sprintf("goos: %s\ngoarch: %s\npkg: example.com/foreigncorrupt\ncpu: T\n", runtime.GOOS, runtime.GOARCH)
			if strings.Contains(pattern, "Subject") {
				// The subject's clean row plus one unparseable line that
				// begins with the sibling's name: corrupt evidence attributed
				// to a benchmark this invocation did not run.
				return []byte(header + "BenchmarkSubject-8 1 5 ns/op\nBenchmarkOther-8 1x 5 ns/op\nPASS\n"), nil
			}
			return []byte(header + "BenchmarkOther-8 1 5 ns/op\nPASS\n"), nil
		},
	}
	var out, errOut bytes.Buffer
	refusal := runPackage(&out, &errOut, e, newGitStateCache(nil), rc, pkgs[0], env, runpkg.Conditions{}, "")
	if refusal == nil {
		t.Fatalf("foreign corrupt evidence reported no error\nstdout:\n%s\nstderr:\n%s", out.String(), errOut.String())
	}
	if !strings.Contains(refusal.Error(), "BenchmarkSubject") || !strings.Contains(refusal.Error(), "which this single-subject invocation did not run") {
		t.Errorf("refusal error = %q, want the subject arm refused on the foreign-named evidence", refusal)
	}
	st := store.New(benchDir)
	if _, err := st.Read("", "BenchmarkSubject", ""); !errors.Is(err, store.ErrNotRecorded) {
		t.Errorf("tainted arm read error = %v, want not recorded", err)
	}
	if _, err := st.Read("", "BenchmarkOther", ""); err != nil {
		t.Errorf("clean arm not recorded: %v", err)
	}
}

// TestRunPerArmScratchSweepIsolation pins the per-arm declared-scratch sweep
// (spec §7.8, §9): a benchmark that leaves its declared scratch behind is
// refused on its own moved state bracket — the hygiene bug surfaces at its
// source — and the leftover is swept (with a printed notice) before the next
// arm's brackets form, so the sibling's recording and manifest are
// independent of sibling order.
func TestRunPerArmScratchSweepIsolation(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/armsweep\n\ngo 1.26.4\n",
		"bench_test.go": "//pew:scratch sb-*\npackage armsweep\n\nimport (\n\t\"os\"\n\t\"path/filepath\"\n\t\"testing\"\n)\n\n" +
			"func BenchmarkLeave(b *testing.B) {\n\td, err := os.MkdirTemp(\".\", \"sb-*\")\n\tif err != nil {\n\t\tb.Fatal(err)\n\t}\n\tif err := os.WriteFile(filepath.Join(d, \"out.txt\"), []byte(\"x\"), 0o644); err != nil {\n\t\tb.Fatal(err)\n\t}\n}\n\n" +
			"func BenchmarkRead(b *testing.B) { _, _ = os.ReadFile(\"input.txt\") }\n",
		"input.txt": "input\n",
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
	if err == nil {
		t.Fatalf("runRun succeeded despite a scratch-leaving arm\nstdout:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "repository state moved during BenchmarkLeave measurement") {
		t.Errorf("stdout does not refuse the scratch-leaving arm on its own bracket:\n%s", out.String())
	}
	// The between-arm sweep removed the leftover, loudly, before the
	// sibling's brackets formed.
	if !strings.Contains(errOut.String(), "swept stale run-scratch") || !strings.Contains(errOut.String(), "sb-") {
		t.Errorf("between-arm sweep notice missing:\n%s", errOut.String())
	}
	st := store.New(benchDir)
	if _, err := st.Read("", "BenchmarkLeave", ""); !errors.Is(err, store.ErrNotRecorded) {
		t.Errorf("scratch-leaving arm read error = %v, want not recorded", err)
	}
	recs, err := st.Read("", "BenchmarkRead", "")
	if err != nil {
		t.Fatalf("sibling arm not recorded: %v", err)
	}
	fp, _, _, ok := fingerprintFromConfig(recs[0].Config)
	if !ok {
		t.Fatal("sibling recording lacks current format")
	}
	manifest, err := base64.RawURLEncoding.DecodeString(fp.RuntimeInputs)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(manifest), "input.txt") {
		t.Errorf("sibling manifest lacks its own read: %s", manifest)
	}
	if strings.Contains(string(manifest), "sb-") {
		t.Errorf("sibling manifest carries the leaving arm's scratch leftover: %s", manifest)
	}
}
