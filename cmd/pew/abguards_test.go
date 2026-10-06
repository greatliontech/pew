package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The real comparative capture — no seam installed — reads the module's
// toolchain and build identity through the pass reader built under pew's
// policy and the process facts beside them: every guard the comparator
// judges is captured, the effective-GOFLAGS read and the capture share
// the pass's one go-env snapshot, including the toolchain identity, runs in
// the module's resolved coordinate: a symlinked checkout reads the
// target, never the link (spec §9's one environment policy).
func TestSideGuardsCaptureTheModulesIdentity(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns the toolchain over a fixture module")
	}
	dir := abFixtureRepo(t)
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	var seen []*exec.Cmd
	prior := captureCommandObserver
	captureCommandObserver = func(cmd *exec.Cmd) { seen = append(seen, cmd) }
	t.Cleanup(func() { captureCommandObserver = prior })
	g, err := abConfig{}.sideGuards(context.Background(), link, filepath.Join(link, "p"), false, testEnvironment(t, os.Environ()))
	if err != nil {
		t.Fatal(err)
	}
	if g.Toolchain == "" || g.BuildConfig == "" || g.Machine == "" || g.RuntimeConfig == "" {
		t.Fatalf("the comparative capture left a guard empty: %+v", g)
	}
	// Every child runs in the resolved coordinate. The toolchain identity
	// comes from the same environment snapshot as the build settings.
	rode := map[string]int{}
	for _, cmd := range seen {
		if cmd.Dir != resolved {
			t.Fatalf("the capture's spawn %v ran in %q, want the module's resolved coordinate %q", cmd.Args, cmd.Dir, resolved)
		}
		if len(cmd.Args) > 1 {
			rode[cmd.Args[1]]++
		}
	}
	// One pass, one snapshot: the effective-GOFLAGS read and the guard
	// capture share the reader, so exactly one go-env child ran.
	if rode["env"] != 1 || rode["version"] != 0 {
		t.Fatalf("the capture's children through the reader were %v, want one shared go-env snapshot", rode)
	}
	// Compare with the toolchain's independent native spelling, not with
	// another invocation of the guard implementation being tested.
	cmd := exec.Command("go", "version")
	cmd.Dir = resolved
	version, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	want := strings.TrimSpace(strings.TrimPrefix(string(version), "go version "))
	if g.Toolchain != want {
		t.Fatalf("captured toolchain = %q, native version identity = %q", g.Toolchain, want)
	}
}
