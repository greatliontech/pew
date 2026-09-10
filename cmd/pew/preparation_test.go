package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/guard"
	"github.com/greatliontech/pew/internal/run"
	"github.com/greatliontech/pew/internal/store"
)

// commitFixture makes dir a committed git repository, the shape every
// run fixture needs for its state bracket.
func commitFixture(t *testing.T, dir string) {
	t.Helper()
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
}

// A label the store cannot name refuses at command entry, before the
// listing (REQ-pew-preparation): in a directory with no module, the
// listing would fail with its own error, so the label's refusal is the
// proof it fired first.
func TestRunRefusesAnInvalidLabelAtEntry(t *testing.T) {
	withWorkingDir(t, t.TempDir())
	cmd := newRunCmd()
	cmd.SetArgs([]string{"--label", "a b"})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "invalid label") {
		t.Fatalf("run --label 'a b' = %v; want the label refusal before any listing", err)
	}
	cmd = newRunCmd()
	cmd.SetArgs([]string{"--bench", "("})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "pattern") {
		t.Fatalf("run --bench '(' = %v; want the pattern refusal before any listing", err)
	}
}

// Every package prepares before any package measures: a later
// package's destination refusal (its recording path occupied by a
// directory) is reported before the first package's measurement, and
// the first package still measures and records (REQ-pew-preparation).
func TestRunPreparesEveryPackageBeforeAnyMeasurement(t *testing.T) {
	if testing.Short() {
		t.Skip("loads two fixture packages through the toolchain with the execute seam stubbed")
	}
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":          "module example.com/prep\n\ngo 1.26.4\n",
		"a/a_test.go":     "package a\n\nimport \"testing\"\n\nfunc BenchmarkA(b *testing.B) {}\n",
		"zlate/z_test.go": "package zlate\n\nimport \"testing\"\n\n//pew:scratch pewtmp\nfunc BenchmarkZ(b *testing.B) {}\n",
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	commitFixture(t, dir)
	// A killed run's leftover in the package that will be refused: the
	// sweep must still visit it, or it enters the sibling's baseline and
	// stamps the sibling's recording dirty.
	leftover := filepath.Join(dir, "zlate", "pewtmp-leftover")
	if err := os.MkdirAll(leftover, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(leftover, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	benchDir := filepath.Join(dir, "benchmarks")
	// The later package's destination is a directory: a refusal the
	// listing and the flags decide.
	occupied, err := store.New(benchDir).Path("zlate", "BenchmarkZ", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(occupied, 0o755); err != nil {
		t.Fatal(err)
	}
	withWorkingDir(t, dir)
	var events []string
	rc := runConfig{
		benchDir: benchDir,
		opts:     run.Options{Count: 1, Benchtime: "1x", Bench: "."},
		throttle: func() run.ThrottleSnapshot { return run.ThrottleSnapshot{"c0": 1} },
		execute: func(moduleDir, pin string, env, args []string) ([]byte, error) {
			for _, a := range args {
				if a == "-c" {
					events = append(events, "build")
					return nil, nil
				}
			}
			events = append(events, "measure")
			return []byte("goos: linux\ngoarch: amd64\npkg: example.com/prep/a\ncpu: T\nBenchmarkA-8 1 5 ns/op\nPASS\n"), nil
		},
	}
	var out bytes.Buffer
	err = runRun(context.Background(), &out, &bytes.Buffer{}, rc, []string{"./..."})
	if err == nil || !strings.Contains(err.Error(), "1 package(s) failed") {
		t.Fatalf("runRun = %v\n%s", err, out.String())
	}
	text := out.String()
	refusal := strings.Index(text, "not a regular file")
	recorded := strings.Index(text, "recorded     example.com/prep/a.BenchmarkA")
	if refusal < 0 || recorded < 0 || refusal > recorded {
		t.Fatalf("the later package's refusal must precede the first package's measurement:\n%s", text)
	}
	if len(events) != 2 || events[0] != "build" || events[1] != "measure" {
		t.Fatalf("executions = %v; want the first package's build and one measurement, nothing for the refused package", events)
	}
	if _, err := os.Stat(leftover); !os.IsNotExist(err) {
		t.Fatalf("the refused package's scratch leftover survived the sweep: %v", err)
	}
	recs, err := store.New(benchDir).Read("a", "BenchmarkA", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := recs[0].GetConfig("dirty"); got != "false" {
		t.Fatalf("the sibling's recording stamped dirty=%q by the refused package's leftover", got)
	}
}

// A measured source under the recording store refuses the moment the
// typed view exists, before the warm-up build and the first arm
// (REQ-pew-preparation).
func TestRunRefusesAStoreCoveredSourceBeforeTheWarmupBuild(t *testing.T) {
	if testing.Short() {
		t.Skip("loads a fixture package through the toolchain with the execute seam stubbed")
	}
	dir := t.TempDir()
	// The package lives under the store itself.
	files := map[string]string{
		"go.mod":                   "module example.com/covered\n\ngo 1.26.4\n",
		"benchmarks/in/in_test.go": "package in\n\nimport \"testing\"\n\nfunc BenchmarkIn(b *testing.B) {}\n",
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	commitFixture(t, dir)
	withWorkingDir(t, dir)
	executions := 0
	rc := runConfig{
		benchDir: filepath.Join(dir, "benchmarks"),
		opts:     run.Options{Count: 1, Benchtime: "1x", Bench: "."},
		throttle: func() run.ThrottleSnapshot { return run.ThrottleSnapshot{} },
		execute: func(moduleDir, pin string, env, args []string) ([]byte, error) {
			executions++
			return nil, nil
		},
	}
	var out bytes.Buffer
	err := runRun(context.Background(), &out, &bytes.Buffer{}, rc, []string{"./benchmarks/in"})
	if err == nil || !strings.Contains(out.String(), "lies under the recording store") {
		t.Fatalf("runRun = %v\n%s", err, out.String())
	}
	if executions != 0 {
		t.Fatalf("a store-covered source still launched %d process(es) before its refusal", executions)
	}
}

// ab prepares every package before any iteration: a later package the
// pattern selects nothing in on the B side refuses before the first
// package's builds and iterations, a pattern that compiles nowhere
// refuses at entry, and the standing binaries live beside the
// repository, never under the temp root (REQ-pew-preparation).
func TestABPreparesEveryPackageBeforeAnyIteration(t *testing.T) {
	if testing.Short() {
		t.Skip("loads the fixture through the toolchain with the build and execute seams stubbed")
	}
	dir := abFixtureRepo(t)
	// A second package, listed after p, whose only benchmark the pattern
	// below does not select.
	late := filepath.Join(dir, "zlate")
	if err := os.MkdirAll(late, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(late, "z_test.go"), []byte("package zlate\n\nimport \"testing\"\n\nfunc BenchmarkOther(b *testing.B) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "late"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	withWorkingDir(t, dir)
	builds, iterations := 0, 0
	var binaryDirs []string
	ac := abConfig{
		bench: "BenchmarkWork", count: 2, ref: "HEAD",
		throttle: func() run.ThrottleSnapshot { return run.ThrottleSnapshot{} },
		build: func(_ string, _ []string, args []string) error {
			builds++
			for i, a := range args {
				if a == "-o" && i+1 < len(args) {
					binaryDirs = append(binaryDirs, filepath.Dir(args[i+1]))
				}
			}
			return nil
		},
		execute: func(string, string, []string, string, []string) ([]byte, error) {
			iterations++
			return nil, nil
		},
		guards: func(string, string, bool, []string) (guard.Guards, error) {
			return guard.Guards{Toolchain: "go1", BuildConfig: "b", Machine: "m", RuntimeConfig: "r"}, nil
		},
	}
	err := runAB(context.Background(), &bytes.Buffer{}, &bytes.Buffer{}, ac, []string{"./p", "./zlate"})
	if err == nil || !strings.Contains(err.Error(), "zlate: pattern \"BenchmarkWork\" selects no benchmark on side A") {
		t.Fatalf("ab with a later package the pattern misses = %v; want its refusal before the first package's iterations", err)
	}
	if iterations != 0 {
		t.Fatalf("%d iterations ran before the later package's refusal; want none", iterations)
	}
	if builds != 2 {
		t.Fatalf("builds = %d; want the first package's two sides prepared, the later package refused before its own", builds)
	}
	for _, d := range binaryDirs {
		if filepath.Dir(d) != filepath.Dir(dir) {
			t.Fatalf("standing binaries at %s; want a directory beside the repository %s", d, dir)
		}
	}
	// A pattern that does not compile refuses at entry, before any
	// listing.
	cmd := newABCmd()
	cmd.SetArgs([]string{"--bench", "("})
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "pattern") {
		t.Fatalf("ab --bench '(' = %v; want the pattern refusal at entry", err)
	}
}

// A recording destination overlapping a source input refuses the
// moment the typed view exists, before the warm-up build: the store's
// package subdirectory is an alias of the package itself, so the
// benchmark's recording path resolves to the file the package embeds
// (REQ-pew-preparation). A source under the store proper is the sibling
// store-covered refusal; the alias is what reaches this one.
func TestRunRefusesAnOverlappingDestinationBeforeTheWarmupBuild(t *testing.T) {
	if testing.Short() {
		t.Skip("loads a fixture package through the toolchain with the execute seam stubbed")
	}
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":              "module example.com/overlap\n\ngo 1.26.4\n",
		"sub/embed.go":        "package sub\n\nimport _ \"embed\"\n\n//go:embed BenchmarkIn.txt\nvar Data string\n",
		"sub/BenchmarkIn.txt": "data\n",
		"sub/sub_test.go":     "package sub\n\nimport \"testing\"\n\nfunc BenchmarkIn(b *testing.B) { _ = Data }\n",
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "benchmarks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join("..", "sub"), filepath.Join(dir, "benchmarks", "sub")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	commitFixture(t, dir)
	withWorkingDir(t, dir)
	executions := 0
	rc := runConfig{
		benchDir: filepath.Join(dir, "benchmarks"),
		opts:     run.Options{Count: 1, Benchtime: "1x", Bench: "."},
		throttle: func() run.ThrottleSnapshot { return run.ThrottleSnapshot{} },
		execute: func(moduleDir, pin string, env, args []string) ([]byte, error) {
			executions++
			return nil, nil
		},
	}
	var out bytes.Buffer
	err := runRun(context.Background(), &out, &bytes.Buffer{}, rc, []string{"./sub"})
	if err == nil || !strings.Contains(out.String(), "overlaps source input") {
		t.Fatalf("runRun = %v\n%s", err, out.String())
	}
	if executions != 0 {
		t.Fatalf("an overlapping destination still launched %d process(es) before its refusal", executions)
	}
}

// run serves what is proven and measures the rest: a second run over
// an unchanged tree measures nothing and says so; --all measures the
// valid benchmark again (REQ-pew-serve-proven).
func TestRunServesValidRecordingsByDefault(t *testing.T) {
	if testing.Short() {
		t.Skip("loads a fixture package through the toolchain and runs its benchmarks once")
	}
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/proven\n\ngo 1.26.4\n",
		// A real benchmark: its loop reaches the harness's pacing (b.Loop),
		// which the engine audits as the harness's own — the serve is no
		// longer confined to empty bodies.
		"bench_test.go": "package proven\n\nimport \"testing\"\n\nfunc BenchmarkHot(b *testing.B) {\n\tfor b.Loop() {\n\t}\n}\n\nfunc BenchmarkCounted(b *testing.B) {\n\tfor i := 0; i < b.N; i++ {\n\t}\n}\n",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	commitFixture(t, dir)
	withWorkingDir(t, dir)
	measurements := 0
	rc := runConfig{
		benchDir: filepath.Join(dir, "benchmarks"),
		opts:     run.Options{Count: 1, Benchtime: "1x", Bench: "."},
		throttle: func() run.ThrottleSnapshot { return run.ThrottleSnapshot{"c0": 1} },
		// The real executor, counted: a valid recording needs the test
		// binary's own log and observation, which a stub cannot forge.
		execute: func(moduleDir, pin string, env, args []string) ([]byte, error) {
			build := false
			for _, a := range args {
				if a == "-c" {
					build = true
				}
			}
			if !build {
				measurements++
			}
			return run.Execute(moduleDir, pin, env, args)
		},
	}
	var out bytes.Buffer
	if err := runRun(context.Background(), &out, &bytes.Buffer{}, rc, []string{"."}); err != nil {
		t.Fatalf("first run: %v\n%s", err, out.String())
	}
	if measurements != 2 {
		t.Fatalf("first run measured %d times; want the two unrecorded benchmarks once each", measurements)
	}
	out.Reset()
	// One typed view per package serves the freshness judgment and the
	// capture: the second run loads exactly once.
	loads := 0
	prior := newViewFor
	newViewFor = func(e *gofresh.Engine, ctx context.Context, subjects []gofresh.Subject, moduleDir string, kind gofresh.Kind) (*gofresh.View, error) {
		loads++
		return prior(e, ctx, subjects, moduleDir, kind)
	}
	defer func() { newViewFor = prior }()
	if err := runRun(context.Background(), &out, &bytes.Buffer{}, rc, []string{"."}); err != nil {
		t.Fatalf("second run: %v\n%s", err, out.String())
	}
	if loads != 1 {
		t.Fatalf("the second run built %d typed views; want the one its judgment and capture share", loads)
	}
	if measurements != 2 || !strings.Contains(out.String(), "valid, nothing to run") {
		t.Fatalf("second run over an unchanged tree measured (%d total) or said nothing:\n%s", measurements, out.String())
	}
	all := rc
	all.all = true
	out.Reset()
	if err := runRun(context.Background(), &out, &bytes.Buffer{}, all, []string{"."}); err != nil {
		t.Fatalf("--all run: %v\n%s", err, out.String())
	}
	if measurements != 4 {
		t.Fatalf("--all measured %d times in total; want both valid benchmarks measured again", measurements)
	}
}

// status parses a package's declarations before building its engine: a
// package with no benchmark pays no engine and reports no engine
// failure, while a package with one still does (REQ-pew-serve-proven's
// serve path is status's; the cost order is the preparation's).
func TestStatusBuildsNoEngineForABenchmarklessPackage(t *testing.T) {
	if testing.Short() {
		t.Skip("loads two fixture packages through the toolchain")
	}
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":              "module example.com/nobench\n\ngo 1.26.4\n",
		"nobench/n.go":        "package nobench\n",
		"withbench/w_test.go": "package withbench\n\nimport \"testing\"\n\nfunc BenchmarkW(b *testing.B) {}\n",
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	commitFixture(t, dir)
	withWorkingDir(t, dir)
	// An unreadable PGO profile fails every engine this environment builds.
	t.Setenv("GOFLAGS", "-pgo="+filepath.Join(dir, "missing.pgo"))
	var out bytes.Buffer
	if err := runStatus(context.Background(), &out, filepath.Join(dir, "benchmarks"), "", false, false, false, []string{"./..."}); err != nil {
		t.Fatal(err)
	}
	errors := strings.Count(out.String(), "error ")
	if errors != 1 || !strings.Contains(out.String(), "example.com/nobench/withbench") || strings.Contains(out.String(), "example.com/nobench/nobench") {
		t.Fatalf("status built an engine for a benchmarkless package, or none for the one with a benchmark:\n%s", out.String())
	}
}
