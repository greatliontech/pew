package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/greatliontech/pew/internal/run"
)

func TestRunOwnsObservationRootsPerInvocation(t *testing.T) {
	if testing.Short() {
		t.Skip("loads a fixture package through the toolchain")
	}
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell toolchain counter")
	}
	dir := twoArmFixture(t)
	// The stub's root-open observation must belong to the analyzed program:
	// an empty benchmark cannot legitimately produce that operation under a
	// supported empty-effect inventory.
	writeFile(t, filepath.Join(dir, "bench_test.go"), `package arms
import ("os"; "testing")
func readRoot() { f, _ := os.Open("/"); if f != nil { _ = f.Close() } }
func BenchmarkFirst(b *testing.B) { readRoot() }
func BenchmarkSecond(b *testing.B) { readRoot() }
`)
	withWorkingDir(t, dir)
	realGo, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	active, count := filepath.Join(bin, "active"), filepath.Join(bin, "queries")
	t.Setenv("PEW_ROOTS_ACTIVE", active)
	t.Setenv("PEW_ROOTS_QUERIES", count)
	t.Setenv("PEW_ROOTS_GO", realGo)
	writeFile(t, filepath.Join(bin, "go"), "#!/bin/sh\nif [ -e \"$PEW_ROOTS_ACTIVE\" ] && [ \"$1\" = env ]; then printf 'query\\n' >> \"$PEW_ROOTS_QUERIES\"; fi\nexec \"$PEW_ROOTS_GO\" \"$@\"\n")
	if err := os.Chmod(filepath.Join(bin, "go"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	stub := armStub(nil)
	invocation, arms := 0, 0
	wantEnvironment := ""
	t.Setenv("PEW_ADMITTED_ENVIRONMENT", "")
	samples := 0
	priorObserver := sampleCommandObserver
	sampleCommandObserver = func(*exec.Cmd) { samples++ }
	t.Cleanup(func() { sampleCommandObserver = priorObserver })
	rc := runConfig{
		all:  true,
		opts: run.Options{Count: 1, Benchtime: "1x", Bench: "."},
		execute: func(moduleDir, pin string, env, args []string) ([]byte, error) {
			found := false
			for _, entry := range env {
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
			for _, arg := range args {
				if log, ok := strings.CutPrefix(arg, "-test.testlogfile="); ok {
					if err := os.Setenv("PEW_ADMITTED_ENVIRONMENT", "changed-after-preparation"); err != nil {
						t.Fatal(err)
					}
					writeFile(t, log, "# test log\nopen /\n")
					writeFile(t, active, "")
				}
			}
			out, err := stub(moduleDir, pin, env, args)
			out = []byte(strings.NewReplacer("goos: linux", "goos: "+runtime.GOOS, "goarch: amd64", "goarch: "+runtime.GOARCH).Replace(string(out)))
			return out, err
		},
		beforePersist: func(string) {
			arms++
			if samples != invocation {
				t.Fatalf("invocation %d, arm %d: %d provenance samples", invocation, arms, samples)
			}
			if err := os.Remove(active); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(count)
			if err != nil || strings.Count(string(data), "query\n") != invocation {
				t.Fatalf("invocation %d, arm %d: root queries %q, error %v", invocation, arms, data, err)
			}
		},
	}
	for invocation = 1; invocation <= 2; invocation++ {
		wantEnvironment = strings.Repeat("admitted", invocation)
		if err := os.Setenv("PEW_ADMITTED_ENVIRONMENT", wantEnvironment); err != nil {
			t.Fatal(err)
		}
		var out, errout bytes.Buffer
		if err := runRun(context.Background(), &out, &errout, rc, []string{"."}); err != nil {
			t.Fatalf("run: %v\n%s\n%s", err, &out, &errout)
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
