package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/greatliontech/pew/internal/run"
	"github.com/greatliontech/pew/internal/store"
)

func TestOrdinaryEffortRefusesBeforeListing(t *testing.T) {
	withWorkingDir(t, t.TempDir())
	// If entry admission disappears, pin derivation cannot find taskset and
	// wins the refusal before runRun's own entry check. No listing is needed.
	t.Setenv("PATH", t.TempDir())
	for _, args := range [][]string{{"--count=0"}, {"--count=-1"}, {"--benchtime="}, {"--benchtime=bogus"}, {"--benchtime=0x"}, {"--pin", "--count=0"}, {"--pin", "--benchtime=0x"}} {
		cmd := newRunCmd()
		cmd.SetArgs(args)
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		if err := cmd.Execute(); err == nil || !(strings.Contains(err.Error(), "count must be positive") || strings.Contains(err.Error(), "benchtime")) {
			t.Fatalf("%v: %v", args, err)
		}
	}
	for _, options := range []run.Options{{Count: 0, Benchtime: "1x"}, {Count: 1, Benchtime: "bogus"}} {
		if err := runRun(context.Background(), &bytes.Buffer{}, &bytes.Buffer{}, runConfig{opts: options}, []string{"./..."}); err == nil || !(strings.Contains(err.Error(), "count must be positive") || strings.Contains(err.Error(), "benchtime")) {
			t.Fatalf("direct invocation: %v", err)
		}
	}
}

func TestInvocationDestinationsCannotBeDirectoriesOfOtherRecordings(t *testing.T) {
	st := store.New(t.TempDir())
	root, err := st.Destinations([]store.Key{{Bench: "BenchmarkWork"}})
	if err != nil {
		t.Fatal(err)
	}
	nested, err := st.Destinations([]store.Key{{PkgRel: "BenchmarkWork.txt", Bench: "BenchmarkOther"}})
	if err != nil {
		t.Fatal(err)
	}
	a := &packagePreparation{pkg: pkgMeta{ImportPath: "example.com/a"}, destinations: root}
	b := &packagePreparation{pkg: pkgMeta{ImportPath: "example.com/b"}, destinations: nested}
	for _, order := range [][]*packagePreparation{{a, b}, {b, a}} {
		if err := admitInvocationDestinations(order); err == nil || !strings.Contains(err.Error(), "required as a directory") || !strings.Contains(err.Error(), "example.com/a.BenchmarkWork") || !strings.Contains(err.Error(), "example.com/b.BenchmarkOther") {
			t.Fatalf("conflicting file/directory destinations admitted: %v", err)
		}
	}
}

func TestLocallyRefusedPackageOwnsNoInvocationDestination(t *testing.T) {
	storeRoot := t.TempDir()
	if err := os.Mkdir(filepath.Join(storeRoot, "BenchmarkBlocked.txt"), 0700); err != nil {
		t.Fatal(err)
	}
	config := runConfig{benchDir: storeRoot, opts: run.Options{Count: 1, Benchtime: "1x", Bench: "."}}
	prepare := func(module, declarations string) (*packagePreparation, error) {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "bench_test.go"), "package bench\nimport \"testing\"\n"+declarations)
		p := pkgMeta{ImportPath: module, Dir: dir, TestGoFiles: []string{"bench_test.go"}}
		p.Module.Path, p.Module.Dir = module, dir
		return preparePackageInputs(config, p)
	}
	a, err := prepare("example.com/a", "func BenchmarkWork(b *testing.B) {}\nfunc BenchmarkBlocked(b *testing.B) {}\n")
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("local refusal: %v", err)
	}
	b, err := prepare("example.com/b", "func BenchmarkWork(b *testing.B) {}\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(a.destinations) != 0 || len(b.destinations) != 1 {
		t.Fatal("local refusal retained a pending write or dropped the independent one")
	}
	if err := admitInvocationDestinations([]*packagePreparation{a, b}); err != nil {
		t.Fatalf("locally refused package blocked an independent recording: %v", err)
	}
}

func TestPrivateRunDefaultEffortAndPreparationAttribution(t *testing.T) {
	if testing.Short() {
		t.Skip("prepares two fixture packages")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/effort\n\ngo 1.27.0\n")
	for _, pkg := range []string{"a", "b"} {
		writeFile(t, filepath.Join(dir, pkg, "bench_test.go"), "package bench\nimport \"testing\"\nfunc BenchmarkWork(b *testing.B) {}\n")
	}
	commitFixture(t, dir)
	withWorkingDir(t, dir)
	owned, deps := testDependencies(t)
	ctx, stop := startReporter(owned, &bytes.Buffer{}, 0)
	defer stop()
	reporter := ctx.Value(reporterKey{}).(*reporter)
	var queryPhase string
	deps.prepare = func(cmd *exec.Cmd) {
		if queryPhase == "" && strings.Join(cmd.Args[1:], " ") == "env -json" {
			reporter.mu.Lock()
			queryPhase = reporter.phase
			reporter.mu.Unlock()
		}
	}
	var budgets []string
	rc := runConfig{all: true, benchDir: filepath.Join(dir, "recordings"), opts: run.Options{Count: 1, Bench: "."}, binary: func(_ context.Context, _, _ string, _ testEnvironmentValue, _ string, args []string) ([]byte, error) {
		for _, arg := range args {
			if value, ok := strings.CutPrefix(arg, "-test.benchtime="); ok {
				budgets = append(budgets, value)
				return nil, errors.New("fixture stops after observing admitted effort")
			}
		}
		t.Fatalf("measurement carried no budget: %q", args)
		return nil, nil
	}}
	if err := runRun(ctx, &bytes.Buffer{}, &bytes.Buffer{}, rc, []string{"./..."}); err == nil {
		t.Fatal("fixture measurement failure lost")
	}
	if len(budgets) != 2 || budgets[0] != "1s" || budgets[1] != "1s" {
		t.Fatalf("default effort = %q", budgets)
	}
	if queryPhase != "preparing build configuration example.com/effort/a (1/2)" {
		t.Fatalf("first package query attributed to %q", queryPhase)
	}
}

func TestInvocationRefusesCrossModuleDestinationCollision(t *testing.T) {
	if testing.Short() {
		t.Skip("lists a two-module workspace")
	}
	root := t.TempDir()
	for _, module := range []string{"a", "b"} {
		dir := filepath.Join(root, module)
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/"+module+"\n\ngo 1.27.0\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "bench_test.go"), []byte("package bench\nimport \"testing\"\nfunc BenchmarkWork(b *testing.B) {}\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "go.work"), []byte("go 1.27.0\nuse (\n./a\n./b\n)\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOWORK", filepath.Join(root, "go.work"))
	commitFixture(t, root)
	withWorkingDir(t, root)
	var phases []string
	ctx, stop := startReporter(context.Background(), &bytes.Buffer{}, 0)
	defer stop()
	ctx.Value(reporterKey{}).(*reporter).onPhase = func(phase string) { phases = append(phases, phase) }
	config := runConfig{benchDir: filepath.Join(root, "recordings"), opts: run.Options{Count: 1, Benchtime: "1x", Bench: "."}, execute: func(string, string, []string, []string) ([]byte, error) {
		t.Fatal("collision started execution")
		return nil, nil
	}}
	err := runRun(ctx, &bytes.Buffer{}, &bytes.Buffer{}, config, []string{"./a", "./b"})
	if err == nil || !strings.Contains(err.Error(), "shared by example.com/a.BenchmarkWork and example.com/b.BenchmarkWork") {
		t.Fatalf("collision: %v", err)
	}
	for _, phase := range phases {
		if strings.HasPrefix(phase, "preparing analysis under") {
			t.Fatalf("collision paid analysis preparation: %v", phases)
		}
	}
	if _, err := os.Stat(config.benchDir); !os.IsNotExist(err) {
		t.Fatalf("collision created its store: %v", err)
	}
}
