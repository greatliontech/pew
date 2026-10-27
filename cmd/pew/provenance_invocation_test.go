package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	gofreshtool "github.com/greatliontech/gofresh/gotool"
	"github.com/greatliontech/pew/internal/compare"
	"github.com/greatliontech/pew/internal/run"
	"github.com/greatliontech/pew/internal/store"
)

func TestProvenanceReadsPreparationSnapshot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell toolchain counter")
	}
	realGo, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	bin, dir := t.TempDir(), t.TempDir()
	count := filepath.Join(bin, "queries")
	t.Setenv("PEW_PREPARE_GO", realGo)
	t.Setenv("PEW_PREPARE_QUERIES", count)
	writeFile(t, filepath.Join(bin, "go"), "#!/bin/sh\nif [ \"$1\" = env ]; then printf 'query\\n' >> \"$PEW_PREPARE_QUERIES\"; fi\nexec \"$PEW_PREPARE_GO\" \"$@\"\n")
	if err := os.Chmod(filepath.Join(bin, "go"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/one-snapshot\n\ngo 1.26.4\n")
	env := testEnvironment(t, nil)
	owned, deps := testDependencies(t)
	ctx := withToolchainSampler(owned)
	queryCount := func() int {
		data, err := os.ReadFile(count)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		return strings.Count(string(data), "query\n")
	}
	base, want := 0, 0
	deps.sample = func(ctx context.Context, dir string, env testEnvironmentValue) (string, error) {
		version, err := sampleGoVersion(ctx, dir, env)
		// Observe before constructing the engine: its own validation may
		// perform separate reads, outside the consumer's preparation pass.
		if got := queryCount() - base; got != want {
			t.Fatalf("build preparation and provenance used %d queries, want %d", got, want)
		}
		return version, err
	}
	for i := 1; i <= 3; i++ {
		if i == 3 {
			ctx = withToolchainSampler(owned)
		}
		base, want = queryCount(), 1
		if i == 2 {
			want = 0
		}
		if _, _, err := newEngineAt(ctx, dir, dir, false, env); err != nil {
			t.Fatal(err)
		}
	}
}

func TestJudgedVerbsShareSamplesOnlyWithinInvocation(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("loads fixture packages through the toolchain")
	}
	for _, verb := range []string{"run", "status", "stat"} {
		t.Run(verb, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/samples\n\ngo 1.26.4\n")
			st := store.New(filepath.Join(dir, "benchmarks"))
			for _, pkg := range []string{"one", "two"} {
				writeFile(t, filepath.Join(dir, pkg, "main.go"), "package main\nfunc main() {}\n")
				writeFile(t, filepath.Join(dir, pkg, "bench_test.go"), "package main\nimport \"testing\"\nfunc BenchmarkSample(b *testing.B) {}\n")
				if verb == "stat" {
					// Separate PGO inputs force distinct engine-cache keys within
					// one module. Stat never builds the profile-consuming binary.
					writeFile(t, filepath.Join(dir, pkg, "default.pgo"), "profile fixture "+pkg+"\n")
				}
				writeStatRecording(t, st, pkg, "BenchmarkSample", 100)
			}
			commitFixture(t, dir)
			ctx, deps := testDependencies(t)
			ctx = withWorkDir(ctx, dir)
			checks := 0
			readers := map[*gofreshtool.EnvReader]int{}
			deps.sample = func(ctx context.Context, dir string, env testEnvironmentValue) (string, error) {
				checks++
				reader, err := preparationReader(ctx, dir, env)
				if err != nil {
					return "", err
				}
				readers[reader]++
				return sampleGoVersion(ctx, dir, env)
			}
			for invocation := 1; invocation <= 2; invocation++ {
				before := checks
				var out, errout bytes.Buffer
				switch verb {
				case "run":
					// Preparation constructs both engines before the deliberately
					// refused build. No benchmark measurements are needed here.
					err := runRun(ctx, &out, &errout, runConfig{all: true,
						opts: run.Options{Count: 1, Benchtime: "1x", Bench: "."},
						execute: func(string, string, []string, []string) ([]byte, error) {
							return nil, errors.New("fixture build refused")
						}}, []string{"./..."})
					if err == nil {
						t.Fatal("fixture build unexpectedly succeeded")
					}
				case "status":
					if err := runStatus(ctx, &out, "", "", false, false, false, []string{"./..."}); err != nil {
						t.Fatal(err)
					}
				case "stat":
					if err := runStat(ctx, &out, &errout, statConfig{opts: compare.DefaultOptions()}, nil); err != nil {
						t.Fatal(err)
					}
				}
				wantChecks := 2
				if verb == "status" {
					wantChecks = 4
				} // invocation preflight, then the two lazy engines
				// Each invocation owns one provenance reader, independently of
				// the session's construction and analysis queries. The reader's
				// physical query memo is pinned by TestProvenanceReadsPreparationSnapshot.
				if checks-before != wantChecks || len(readers) != invocation {
					t.Fatalf("invocation %d: %d provenance checks, %d readers; want %d checks and %d readers\n%s\n%s", invocation, checks-before, len(readers), wantChecks, invocation, &out, &errout)
				}
				for _, uses := range readers {
					if uses != wantChecks {
						t.Fatalf("preparation reader used %d times; want one invocation's %d checks", uses, wantChecks)
					}
				}
			}
		})
	}
}

func TestInvocationSharesModuleSessionAcrossPackages(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/shared-session\n\ngo 1.26.4\n")
	pkgA := filepath.Join(dir, "a")
	pkgB := filepath.Join(dir, "b")
	if err := os.MkdirAll(pkgA, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(pkgB, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, _, err := beginInvocation(t.Context(), "", nil, &bytes.Buffer{})
	if err != nil {
		t.Fatal(err)
	}
	env := testEnvironment(t, nil)
	eA, err := buildEngine(ctx, dir, pkgA, env, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	eB, err := buildEngine(ctx, dir, pkgB, env, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	if eA.build != eB.build {
		t.Fatal("packages in the same module with identical build inputs did not share *gofresh.Session")
	}
	if eA.runtime.Directory != pkgA || eB.runtime.Directory != pkgB {
		t.Fatalf("runtime directories = (%q, %q), want (%q, %q)", eA.runtime.Directory, eB.runtime.Directory, pkgA, pkgB)
	}
	ePGO, err := buildEngine(ctx, dir, pkgA, env, nil, "pgo:deadbeef")
	if err != nil {
		t.Fatal(err)
	}
	if ePGO.build == eA.build {
		t.Fatal("distinct PGO build input reused *gofresh.Session")
	}
}
