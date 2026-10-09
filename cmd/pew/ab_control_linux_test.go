//go:build linux

package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestABInterruptedAddRetainsCleanupOwnership(t *testing.T) {
	repo := abFixtureRepo(t)
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	ready := filepath.Join(bin, "ready")
	script := "#!/bin/sh\nif [ \"$1\" = worktree ] && [ \"$2\" = add ]; then\n \"$PEW_REAL_GIT\" \"$@\" || exit $?\n printf ready > \"$PEW_READY\"\n sleep 600 &\n wait\nelse\n exec \"$PEW_REAL_GIT\" \"$@\"\nfi\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PEW_REAL_GIT", realGit)
	t.Setenv("PEW_READY", ready)
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	g := abGitControl{env: testEnvironment(t, nil)}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	type result struct {
		dir     string
		cleanup func(context.Context) error
		err     error
	}
	done := make(chan result, 1)
	go func() {
		dir, cleanup, err := g.add(ctx, repo, filepath.Dir(repo), "HEAD")
		done <- result{dir, cleanup, err}
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("native add did not complete before wrapper blocked")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	var got result
	select {
	case got = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("add did not cancel")
	}
	if got.cleanup == nil || !errors.Is(got.err, context.Canceled) || !strings.Contains(got.err.Error(), "uncertain residue") {
		t.Fatalf("lost interrupted-add ownership: %+v", got)
	}
	gate, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer stop()
	if err := got.cleanup(gate); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(got.dir); !os.IsNotExist(err) {
		t.Fatalf("completed add residue retained: %v", err)
	}
	if _, err := os.Stat(got.dir + ".owner"); !os.IsNotExist(err) {
		t.Fatalf("ownership residue retained: %v", err)
	}
}

func TestABNativeGitCancellationContainsDescendants(t *testing.T) {
	for _, args := range [][]string{{"rev-parse", "--show-toplevel"}, {"rev-parse", "--git-common-dir"}, {"worktree", "list", "--porcelain", "-z"}, {"worktree", "add"}, {"worktree", "remove"}} {
		t.Run(strings.Join(args, "-"), func(t *testing.T) {
			root := t.TempDir()
			pidPath := filepath.Join(root, "child")
			wrapper := filepath.Join(root, "git")
			if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nsleep 600 &\nprintf '%s' \"$!\" > \"$PEW_CHILD\"\nwait\n"), 0o755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", root+":"+os.Getenv("PATH"))
			t.Setenv("PEW_CHILD", pidPath)
			g := abGitControl{env: testEnvironment(t, nil)}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := g.command(ctx, root, args...); done <- err }()
			deadline := time.Now().Add(5 * time.Second)
			var pid int
			for time.Now().Before(deadline) {
				b, err := os.ReadFile(pidPath)
				if err == nil {
					pid, _ = strconv.Atoi(string(b))
					if pid > 0 {
						break
					}
				}
				time.Sleep(10 * time.Millisecond)
			}
			if pid == 0 {
				t.Fatal("wrapper did not launch descendant")
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("lost cancellation: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("native Git outlived cancellation")
			}
			for time.Now().Before(deadline) {
				b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
				if os.IsNotExist(err) || strings.Contains(string(b), ") Z ") {
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
			t.Fatalf("Git descendant %d survived", pid)
		})
	}
}

func TestABCleanupRefusesForeignOwnershipAndReportsFailure(t *testing.T) {
	repo := abFixtureRepo(t)
	g := abGitControl{env: testEnvironment(t, nil)}
	dir, cleanup, err := g.add(t.Context(), repo, filepath.Dir(repo), "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	ownerPath := dir + ".owner"
	owner, err := os.ReadFile(ownerPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ownerPath, []byte(`{"Repository":"foreign","PID":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cleanup(t.Context()); err == nil {
		t.Fatal("foreign ownership removed")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatal("foreign worktree lost")
	}
	if err := os.WriteFile(ownerPath, owner, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := cleanup(ctx); err == nil || !strings.Contains(err.Error(), "residue") {
		t.Fatalf("cleanup failure hidden: %v", err)
	}
	if _, err := g.command(t.Context(), repo, "worktree", "lock", dir); err != nil {
		t.Fatal(err)
	}
	if err := cleanup(t.Context()); err == nil || !strings.Contains(err.Error(), "cleanup failed; retained") {
		t.Fatalf("native remove failure hidden: %v", err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatal("failed native remove lost worktree")
	}
	if _, err := g.command(t.Context(), repo, "worktree", "unlock", dir); err != nil {
		t.Fatal(err)
	}
	if err := cleanup(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("owned worktree retained: %v", err)
	}
	// An empty name-shaped directory without an owner is never enough evidence.
	unowned := filepath.Join(filepath.Dir(repo), ".pew-ab-worktree-unowned")
	if err := os.Mkdir(unowned, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := g.sweep(t.Context(), repo, filepath.Dir(repo)); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(unowned); err != nil {
		t.Fatal("unowned empty directory deleted")
	}
}
