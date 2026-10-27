package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	git "github.com/go-git/go-git/v5"
	"github.com/greatliontech/gofresh/guard"
	"github.com/greatliontech/pew/internal/run"
)

func TestABTimedAndDiagnosticExecutionsShareInheritedHarnessPolicy(t *testing.T) {
	if testing.Short() {
		t.Skip("executes both reference builds and their diagnostics")
	}
	for _, moved := range []bool{false, true} {
		t.Run(map[bool]string{false: "matching", true: "comparison-guard-moved"}[moved], func(t *testing.T) {
			root := abFixtureRepo(t)
			writeFile(t, filepath.Join(root, "p", "p_test.go"), `package p
import "testing"
var sink []byte
//gofresh:pure
func BenchmarkWork(b *testing.B) {
 name := map[bool]string{false: "large", true: "small"}[testing.Short()]
 b.Run(name, func(b *testing.B) { for b.Loop() { sink = make([]byte, 2<<20) } })
}
`)
			repo, err := git.PlainOpen(root)
			if err != nil {
				t.Fatal(err)
			}
			commitAll(t, repo, "conditional diagnostic workload")
			ctx := withWorkingDir(t, root)
			t.Setenv("GOFLAGS", "-short -v=0 -parallel=0x2")
			ac := abConfig{bench: "^BenchmarkWork$", count: 1, benchtime: "1x", ref: "HEAD", profile: "alloc", profileBenchtime: "32x", out: filepath.Join(t.TempDir(), "ab.txt")}
			timed, diagnostics := 0, 0
			check := func(args []string) {
				t.Helper()
				for _, want := range []string{"-test.short=true", "-test.v=false", "-test.parallel=2"} {
					if !slices.Contains(args, want) {
						t.Fatalf("execution lost inherited %s: %v", want, args)
					}
				}
			}
			ac.execute = func(dir, pin string, values []string, binary string, args []string) ([]byte, error) {
				timed++
				check(args)
				return run.ExecuteBinaryContext(ctx, dir, pin, testEnvironment(t, values), binary, args)
			}
			ac.diagnostic = func(ctx context.Context, dir, pin string, env testEnvironmentValue, binary string, args []string, errw io.Writer) ([]byte, error) {
				diagnostics++
				check(args)
				return run.ExecuteDiagnostic(ctx, dir, pin, env, binary, args, errw)
			}
			ac.guards = func(ctx context.Context, moduleDir, pkgDir string, main bool, values []string) (guard.Guards, error) {
				g, err := (abConfig{}).sideGuards(ctx, moduleDir, pkgDir, main, testEnvironment(t, values))
				if moved && diagnostics == 2 {
					g.BuildConfig += "changed-at-diagnostic-close"
				}
				return g, err
			}
			var out, errout bytes.Buffer
			err = runAB(ctx, &out, &errout, ac, []string{"./p"})
			if timed != 2 || diagnostics != 2 {
				t.Fatalf("execution population timed=%d diagnostics=%d, error=%v", timed, diagnostics, err)
			}
			if !moved && err != nil {
				t.Fatalf("compatible child workload refused: %v\n%s\n%s", err, &out, &errout)
			}
			if moved && (err == nil || !strings.Contains(err.Error(), "comparison guards changed since its timed build")) {
				t.Fatalf("live comparison borrowed earlier build guards: %v\n%s", err, &out)
			}
			artifact, readErr := os.ReadFile(ac.out)
			if readErr != nil {
				t.Fatal(readErr)
			}
			wantProfiles := 2
			if moved {
				wantProfiles = 1
			}
			if strings.Count(string(artifact), "pew-ab-section: measurement") != 2 || strings.Count(string(artifact), "pew-ab-section: profile") != wantProfiles || !bytes.Contains(artifact, []byte("Work/small")) {
				t.Fatalf("completed sides or matched workload lost: %s", artifact)
			}
		})
	}
}
