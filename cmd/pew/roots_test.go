package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/greatliontech/pew/internal/gotool"
	"github.com/greatliontech/pew/internal/run"
)

func TestRunOwnsObservationRootsPerInvocation(t *testing.T) {
	if testing.Short() {
		t.Skip("loads a fixture package through the toolchain")
	}
	dir := twoArmFixture(t)
	// A subpackage separates the classification-root query's process cwd
	// from the analysis/build queries' module cwd, without guessing their cost.
	pkgdir := filepath.Join(dir, "p")
	writeFile(t, filepath.Join(pkgdir, "bench_test.go"), `package arms
import ("os"; "testing")
func readRoot() { f, _ := os.Open("/"); if f != nil { _ = f.Close() } }
func BenchmarkFirst(b *testing.B) { readRoot() }
func BenchmarkSecond(b *testing.B) { readRoot() }
`)
	if err := os.Remove(filepath.Join(dir, "bench_test.go")); err != nil {
		t.Fatal(err)
	}
	invocation, arms := 0, 0
	wantEnvironment := ""
	t.Setenv("PEW_ADMITTED_ENVIRONMENT", "")
	rootQueries := 0
	ctx, deps := testDependencies(t)
	ctx = withWorkDir(ctx, dir)
	deps.prepare = func(cmd *exec.Cmd) {
		if cmd.Dir == gotool.CommandDir(pkgdir) && slices.Equal(cmd.Args[1:], []string{"env", "-json"}) {
			rootQueries++
		}
	}
	rc := runConfig{
		all:  true,
		opts: run.Options{Count: 1, Benchtime: "1x", Bench: "."},
		binary: func(ctx context.Context, dir, pin string, env gotool.Environment, binary string, args []string) ([]byte, error) {
			arms++
			found := false
			for _, entry := range env.Values() {
				if value, ok := strings.CutPrefix(entry, "PEW_ADMITTED_ENVIRONMENT="); ok {
					found = true
					if value != wantEnvironment {
						t.Fatalf("measurement inherited after preparation: %q, want %q", value, wantEnvironment)
					}
				}
			}
			if !found {
				t.Fatal("measurement lost the admitted environment")
			}
			if err := os.Setenv("PEW_ADMITTED_ENVIRONMENT", "changed-after-preparation"); err != nil {
				t.Fatal(err)
			}
			return run.ExecuteBinaryContext(ctx, dir, pin, env, binary, args)
		},
	}
	for invocation = 1; invocation <= 2; invocation++ {
		wantEnvironment = strings.Repeat("admitted", invocation)
		if err := os.Setenv("PEW_ADMITTED_ENVIRONMENT", wantEnvironment); err != nil {
			t.Fatal(err)
		}
		var out, errout bytes.Buffer
		if err := runRun(ctx, &out, &errout, rc, []string{"./p"}); err != nil {
			t.Fatalf("run: %v\n%s\n%s", err, &out, &errout)
		}
		if rootQueries != invocation {
			t.Fatalf("invocation %d: %d package-root queries, want one per invocation shared by both arms", invocation, rootQueries)
		}
		if strings.Count(errout.String(), `open "/" in `) != 2 || !strings.Contains(errout.String(), "external directory input: /") {
			t.Fatalf("observation refusals lost their producing operations:\n%s", &errout)
		}
		if strings.Count(errout.String(), "is identity-only:") != 2 {
			t.Fatalf("classification refusal suppressed missing outcome support:\n%s", &errout)
		}
	}
	if arms != 4 {
		t.Fatalf("persisted %d arms, want four across two invocations", arms)
	}
}
