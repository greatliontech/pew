package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/greatliontech/pew/internal/gotool"
	"github.com/greatliontech/pew/internal/run"
)

func TestABIncompatibleProfilesAndCleanupFailurePrecedence(t *testing.T) {
	if testing.Short() {
		t.Skip("real two-side captures and native worktree finalization")
	}
	if runtime.GOOS == "windows" {
		t.Skip("POSIX native-Git wrapper for cleanup interruption")
	}
	for _, interrupt := range []bool{false, true} {
		t.Run(map[bool]string{false: "cleanup-failure", true: "interrupted-cleanup"}[interrupt], func(t *testing.T) {
			root := abFixtureRepo(t)
			source := `package p
import "testing"
var sink []byte
//gofresh:pure
func BenchmarkWork(b *testing.B) { b.Run("base",func(b *testing.B){for i:=0;i<b.N;i++{sink=make([]byte,1<<20)}}) }
`
			writeFile(t, filepath.Join(root, "p", "p_test.go"), source)
			repo, err := git.PlainOpen(root)
			if err != nil {
				t.Fatal(err)
			}
			commitAll(t, repo, "base child")
			writeFile(t, filepath.Join(root, "p", "p_test.go"), strings.Replace(source, `"base"`, `"new"`, 1))
			withWorkingDir(t, root)
			realGit, err := exec.LookPath("git")
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			watchDone := make(chan error, 1)
			stopWatch := make(chan struct{})
			defer close(stopWatch)
			if interrupt {
				bin := t.TempDir()
				ready, release := filepath.Join(bin, "ready"), filepath.Join(bin, "release")
				script := "#!/bin/sh\nif [ \"$1\" = worktree ] && [ \"$2\" = remove ]; then\n printf ready > \"$PEW_READY\"\n while [ ! -f \"$PEW_RELEASE\" ]; do sleep 0.01; done\nfi\nexec \"$PEW_REAL_GIT\" \"$@\"\n"
				if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
					t.Fatal(err)
				}
				t.Setenv("PEW_REAL_GIT", realGit)
				t.Setenv("PEW_READY", ready)
				t.Setenv("PEW_RELEASE", release)
				t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
				go func() {
					ticker := time.NewTicker(10 * time.Millisecond)
					defer ticker.Stop()
					for {
						select {
						case <-stopWatch:
							return
						case <-ticker.C:
							if _, err := os.Stat(ready); err == nil {
								cancel()
								watchDone <- os.WriteFile(release, nil, 0o600)
								return
							}
						}
					}
				}()
			}
			env := testEnvironment(t, nil)
			locked := ""
			t.Cleanup(func() {
				if locked == "" {
					return
				}
				gate, stop := context.WithTimeout(context.Background(), 10*time.Second)
				defer stop()
				for _, args := range [][]string{{"worktree", "unlock", locked}, {"worktree", "remove", "--force", locked}} {
					if _, err := run.ExecuteDiagnostic(gate, root, "", env, realGit, args, io.Discard); err != nil {
						t.Errorf("fixture cleanup: %v", err)
					}
				}
				if err := os.Remove(locked + ".owner"); err != nil {
					t.Errorf("fixture owner cleanup: %v", err)
				}
			})
			ac := abConfig{bench: "^BenchmarkWork$", count: 1, benchtime: "1x", ref: "HEAD", profile: "alloc", profileBenchtime: "30x", jsonOut: true, out: filepath.Join(t.TempDir(), "ab.txt")}
			captures := 0
			ac.diagnostic = func(c context.Context, dir, pin string, e gotool.Environment, bin string, args []string, diagnostics io.Writer) ([]byte, error) {
				data, err := run.ExecuteDiagnostic(c, dir, pin, e, bin, args, diagnostics)
				if err != nil {
					return data, err
				}
				captures++
				if captures == 2 {
					control := abGitControl{env: e}
					locked, err = control.topLevel(c, dir)
					if err == nil {
						_, err = control.command(c, root, "worktree", "lock", locked)
					}
				}
				return data, err
			}
			var out, diagnostics bytes.Buffer
			err = runAB(ctx, &out, &diagnostics, ac, []string{"./p"})
			want := 1
			if interrupt {
				want = 130
			}
			var unfulfilled *profileUnfulfilledError
			if err == nil || exitCode(err) != want || !errors.As(err, &unfulfilled) || !strings.Contains(err.Error(), "requested profile comparison unfulfilled") || !strings.Contains(err.Error(), "cleanup failed; retained") {
				t.Fatalf("finalization error=%v want exit=%d\n%s\n%s", err, want, &out, &diagnostics)
			}
			if interrupt {
				select {
				case err := <-watchDone:
					if err != nil {
						t.Fatal(err)
					}
				default:
					t.Fatal("interruption did not occur during cleanup")
				}
			}
			reports, _ := decodeProfileReport(t, out.Bytes())
			if len(reports) != 1 || reports[0].Compared || reports[0].Base.Integrity != "verified" || reports[0].New.Integrity != "verified" || !strings.Contains(reports[0].Reason, "children") {
				t.Fatalf("completed incompatible captures lost: %s", &out)
			}
			artifact, err := os.ReadFile(ac.out)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Count(artifact, []byte("pew-ab-section: profile\n")) != 2 || bytes.Count(artifact, []byte("pew-ab-section: measurement\n")) != 2 {
				t.Fatal("cleanup failure discarded completed artifact units")
			}
			if _, err := os.Stat(locked); err != nil {
				t.Fatalf("failed cleanup falsely reported residue: %v", err)
			}
		})
	}
}
