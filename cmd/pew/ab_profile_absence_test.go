package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	git "github.com/go-git/go-git/v5"
	"github.com/greatliontech/pew/internal/gotool"
	"github.com/greatliontech/pew/internal/profiles"
	"github.com/greatliontech/pew/internal/run"
)

func TestABMissingDiagnosticSidesRemainUnfulfilled(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("captures real nonempty profiles for distinct A/B populations")
	}
	root := abFixtureRepo(t)
	path := filepath.Join(root, "p", "p_test.go")
	source := `package p
import "testing"
var sink []byte
//gofresh:pure
func BenchmarkBefore(b *testing.B) { for b.Loop() { sink = make([]byte, 2<<20) } }
//gofresh:pure
func BenchmarkShared(b *testing.B) { for b.Loop() { sink = make([]byte, 2<<20) } }
`
	writeFile(t, path, source)
	repo, err := git.PlainOpen(root)
	if err != nil {
		t.Fatal(err)
	}
	commitAll(t, repo, "reference population")
	writeFile(t, path, strings.ReplaceAll(source, "BenchmarkBefore", "BenchmarkAfter"))
	baseCtx := withWorkingDir(t, root)
	for _, mode := range []string{"text", "json", "failure", "interrupt", "temporary"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(baseCtx)
			defer cancel()
			ac := abConfig{bench: ".", count: 1, benchtime: "1x", ref: "HEAD", profile: "alloc", profileBenchtime: "16x", jsonOut: mode != "text", out: filepath.Join(t.TempDir(), "ab.txt")}
			if mode == "temporary" {
				ac.out = ""
			}
			if mode == "failure" {
				// No later capture may reinstall the failed side's executable
				// before its continued retention is checked.
				ac.bench = "^Benchmark(Before|After)$"
			}
			calls := 0
			executables := map[string]string{}
			ac.diagnostic = func(ctx context.Context, dir, pin string, env gotool.Environment, bin string, args []string, w io.Writer) ([]byte, error) {
				calls++
				binary, err := os.ReadFile(bin)
				if err != nil {
					return nil, err
				}
				executables[bin] = profiles.Digest(binary)
				if mode == "failure" && calls == 2 {
					return nil, fmt.Errorf("diagnostic sentinel")
				}
				data, err := run.ExecuteDiagnostic(ctx, dir, pin, env, bin, args, w)
				if mode == "interrupt" && err == nil {
					cancel()
				}
				return data, err
			}
			var out bytes.Buffer
			err := runAB(ctx, &out, io.Discard, ac, []string{"./p"})
			want, captures := 2, 4
			if mode == "failure" {
				want, captures = 1, 1
			}
			if mode == "interrupt" {
				want, captures = 130, 1
			}
			if err == nil || exitCode(err) != want {
				t.Fatalf("exit=%v, want %d\n%s", err, want, &out)
			}
			if mode == "failure" && !strings.Contains(err.Error(), "diagnostic sentinel") {
				t.Fatalf("lost operational cause: %v", err)
			}
			if ac.out != "" {
				artifact, readErr := os.ReadFile(ac.out)
				if readErr != nil || bytes.Count(artifact, []byte("pew-ab-section: measurement\n")) != 2 || bytes.Count(artifact, []byte("pew-ab-section: profile\n")) != captures {
					t.Fatalf("completed prefix: %v\n%s", readErr, artifact)
				}
			}
			wantCalls := 2
			if mode == "temporary" {
				wantCalls = 4 // each capture owns its own unretained directory
			}
			if mode == "interrupt" {
				wantCalls = 1
			}
			if len(executables) != wantCalls {
				t.Fatalf("executables=%v", executables)
			}
			for path, digest := range executables {
				if filepath.Ext(path) != ".exe" {
					t.Fatalf("diagnostic path cannot use Windows executable lookup: %s", path)
				}
				binary, err := os.ReadFile(path)
				if mode == "temporary" {
					if !os.IsNotExist(err) {
						t.Fatalf("unretained executable survived: %s %v", path, err)
					}
				} else if err != nil || profiles.Digest(binary) != digest || filepath.Base(path) != digest+".exe" {
					t.Fatalf("retained executable changed or vanished: %s %v", path, err)
				}
			}
			if mode == "text" {
				if !strings.Contains(out.String(), "no completed measurement on this side") || !strings.Contains(out.String(), "compared=false") {
					t.Fatalf("missing text disposition: %s", &out)
				}
			} else if mode == "json" {
				reports, _ := decodeProfileReport(t, out.Bytes())
				if len(reports) != 3 {
					t.Fatalf("reports=%+v", reports)
				}
				for _, r := range reports {
					if r.Benchmark == "BenchmarkShared" {
						if !r.Compared || r.Base.Integrity != "verified" || r.New.Integrity != "verified" {
							t.Fatalf("shared comparison lost beside absent sides: %+v", r)
						}
						continue
					}
					present, absent := r.New, r.Base
					if r.Benchmark == "BenchmarkBefore" {
						present, absent = r.Base, r.New
					}
					if r.Compared || absent.Integrity != "unavailable" || absent.Error != "" || absent.Reason != "no completed measurement on this side" || present.Integrity != "verified" || present.Empty == nil || *present.Empty {
						t.Fatalf("wrong per-side disposition: %+v", r)
					}
				}
			}
		})
	}
}
