package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/greatliontech/pew/internal/run"
)

// abConfigStub is the executor-stubbed ab configuration the placement
// tests drive: builds and executions never run the toolchain.
func abConfigStub(worktreeDir string) abConfig {
	return abConfig{
		bench: ".", count: 1, ref: "HEAD", worktreeDir: worktreeDir,
		throttle: func() run.ThrottleSnapshot { return run.ThrottleSnapshot{} },
		build:    func(string, []string, []string) error { return nil },
		execute: func(execDir, pin string, env []string, bin string, args []string) ([]byte, error) {
			return []byte("BenchmarkWork-8 1000 90 ns/op\n"), nil
		},
	}
}

// --worktree-dir places side B's worktree and both binaries in the
// operator's same-filesystem directory instead of beside the
// repository; a placement on another filesystem is refused, never
// silently degraded (spec §12).
func TestABWorktreeDirPlacesSideBWhereTheOperatorNames(t *testing.T) {
	dir := abFixtureRepo(t)
	prior, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(filepath.Join(dir, "p")); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(prior)
	placement := filepath.Join(t.TempDir(), "scratch")
	if err := os.MkdirAll(placement, 0o755); err != nil {
		t.Fatal(err)
	}
	ac := abConfigStub(placement)
	var seenDirs []string
	ac.build = func(buildDir string, env []string, args []string) error {
		for i, a := range args {
			if a == "-o" && i+1 < len(args) {
				seenDirs = append(seenDirs, filepath.Dir(args[i+1]))
			}
		}
		return nil
	}
	var out, errOut bytes.Buffer
	if err := runAB(context.Background(), &out, &errOut, ac, []string{"."}); err != nil {
		t.Fatalf("runAB: %v\nstderr: %s", err, errOut.String())
	}
	if len(seenDirs) != 2 {
		t.Fatalf("builds = %v, want both sides", seenDirs)
	}
	for _, d := range seenDirs {
		if rel, err := filepath.Rel(placement, d); err != nil || !filepath.IsLocal(rel) || !strings.HasPrefix(filepath.Base(d), ".pew-ab-bin-") {
			t.Fatalf("binaries built at %s, want under --worktree-dir %s", d, placement)
		}
	}
	entries, err := os.ReadDir(placement)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("placement holds residue after a clean run: %v", entries)
	}
	if _, err := os.Stat(filepath.Join(placement, "nope")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if _, err := abPlacement(dir, filepath.Join(placement, "missing")); err == nil || !strings.Contains(err.Error(), "--worktree-dir") {
		t.Fatalf("absent --worktree-dir = %v; want its refusal", err)
	}
	// Inside the repository the placement would dirty the working tree
	// and put the sweep's RemoveAll inside it: refused, the root itself
	// and a subdirectory alike.
	inside := filepath.Join(dir, "scratch")
	if err := os.MkdirAll(inside, 0o755); err != nil {
		t.Fatal(err)
	}
	// A symlink outside the repository that resolves into it is the
	// same placement: refused on the physical path.
	link := filepath.Join(t.TempDir(), "into-repo")
	if err := os.Symlink(inside, link); err != nil {
		t.Fatal(err)
	}
	for _, candidate := range []string{dir, inside, link} {
		if _, err := abPlacement(dir, candidate); err == nil || !strings.Contains(err.Error(), "inside the repository") {
			t.Fatalf("in-repository --worktree-dir %s = %v; want the refusal", candidate, err)
		}
	}
	// A placement on another filesystem refuses — where this host offers
	// one (a tmpfs beside a disk-backed repository).
	for _, candidate := range []string{"/dev/shm", os.TempDir()} {
		if same, err := sameDevice(dir, candidate); err == nil && !same {
			if _, err := abPlacement(dir, candidate); err == nil || !strings.Contains(err.Error(), "different filesystem") {
				t.Fatalf("cross-device --worktree-dir %s = %v; want the refusal", candidate, err)
			}
			return
		}
	}
	t.Log("no cross-device directory available on this host; the refusal leg did not run")
}

// A killed run's side-B worktree beside the repository is swept at the
// next run's start — only the ones this repository minted (their .git
// file points into its common directory) that `git worktree list` no
// longer registers; a registered worktree (a run in flight) survives,
// and so does a sibling repository's worktree in the same placement,
// registered or not (spec §12).
func TestABSweepsStaleSideBWorktrees(t *testing.T) {
	dir := abFixtureRepo(t)
	placement := filepath.Dir(dir)
	git := func(repo string, args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	// Residue: a worktree this repository added whose registration a
	// crash lost (its .git file still names this repository's gitdir).
	mintResidue := func() string {
		t.Helper()
		stale, err := os.MkdirTemp(placement, ".pew-ab-worktree-*")
		if err != nil {
			t.Fatal(err)
		}
		git(dir, "worktree", "add", "-q", "--detach", stale, "HEAD")
		// The registration lives under a sanitized name: find it by the
		// gitdir it records, then drop it as a crash would.
		entries, err := filepath.Glob(filepath.Join(dir, ".git", "worktrees", "*"))
		if err != nil {
			t.Fatal(err)
		}
		dropped := false
		for _, entry := range entries {
			gitdir, err := os.ReadFile(filepath.Join(entry, "gitdir"))
			if err == nil && strings.TrimSpace(string(gitdir)) == filepath.Join(stale, ".git") {
				if err := os.RemoveAll(entry); err != nil {
					t.Fatal(err)
				}
				dropped = true
			}
		}
		if !dropped {
			t.Fatalf("no registration found for %s among %v", stale, entries)
		}
		return stale
	}
	stale := mintResidue()
	live, err := os.MkdirTemp(placement, ".pew-ab-worktree-*")
	if err != nil {
		t.Fatal(err)
	}
	git(dir, "worktree", "add", "-q", "--detach", live, "HEAD")
	defer exec.Command("git", "-C", dir, "worktree", "remove", "--force", live).Run()
	// A sibling repository's live worktree in the shared placement, and
	// a bare directory of the residue shape that no repository owns.
	sibling := filepath.Join(placement, "sibling")
	if err := os.MkdirAll(sibling, 0o755); err != nil {
		t.Fatal(err)
	}
	git(sibling, "init", "-q")
	git(sibling, "-c", "user.email=t@example.com", "-c", "user.name=t", "commit", "-q", "--allow-empty", "-m", "base")
	foreign, err := os.MkdirTemp(placement, ".pew-ab-worktree-*")
	if err != nil {
		t.Fatal(err)
	}
	git(sibling, "worktree", "add", "-q", "--detach", foreign, "HEAD")
	defer exec.Command("git", "-C", sibling, "worktree", "remove", "--force", foreign).Run()
	// A run killed after minting its directory and before git wrote the
	// .git file leaves an empty directory: nobody's, and swept. A
	// non-empty directory with no .git file is someone else's.
	minted := filepath.Join(placement, ".pew-ab-worktree-minted")
	if err := os.MkdirAll(minted, 0o755); err != nil {
		t.Fatal(err)
	}
	nobodys := filepath.Join(placement, ".pew-ab-worktree-nobodys")
	if err := os.MkdirAll(nobodys, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nobodys, "keep"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	swept := sweepStaleWorktrees(dir, placement)
	want := []string{minted, stale}
	sort.Strings(swept)
	sort.Strings(want)
	if !slices.Equal(swept, want) {
		t.Fatalf("swept = %v, want this repository's unregistered residue and the empty mint alone", swept)
	}
	if _, err := os.Stat(minted); !os.IsNotExist(err) {
		t.Fatalf("empty mint survived the sweep: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("the stale worktree survived the sweep")
	}
	for name, path := range map[string]string{"registered": live, "sibling repository's": foreign, "unowned": nobodys} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("the %s worktree was swept: %v", name, err)
		}
	}
	// End to end: a run reports the sweep on stderr.
	stale = mintResidue()
	prior, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(filepath.Join(dir, "p")); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(prior)
	var out, errOut bytes.Buffer
	if err := runAB(context.Background(), &out, &errOut, abConfigStub(""), []string{"."}); err != nil {
		t.Fatalf("runAB: %v\nstderr: %s", err, errOut.String())
	}
	if !strings.Contains(errOut.String(), "swept a stale side-B worktree "+stale) {
		t.Fatalf("the run did not report the sweep:\n%s", errOut.String())
	}
}
