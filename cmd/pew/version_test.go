package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh/gotool"
)

func TestVersionWorksWithoutAProject(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("malformed module\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := withWorkingDir(t, dir)
	t.Setenv("PATH", t.TempDir())
	var first string
	for _, args := range [][]string{{"version"}, {"--version"}} {
		root := newRootCmd()
		var out, stderr bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&stderr)
		root.SetArgs(args)
		if err := root.ExecuteContext(ctx); err != nil {
			t.Fatal(err)
		}
		got := out.String()
		if !strings.HasPrefix(got, "pew ") || strings.Count(got, "\n") != 1 || stderr.Len() != 0 {
			t.Fatalf("%v: %q / %q", args, got, stderr.String())
		}
		for _, field := range []string{"revision=", "modified=", "go=", "gofresh="} {
			if !strings.Contains(got, field) {
				t.Fatalf("missing %s: %q", field, got)
			}
		}
		if first != "" && got != first {
			t.Fatalf("version forms disagree: %q / %q", first, got)
		}
		first = got
	}
	root := newRootCmd()
	root.SetArgs([]string{"version", "unexpected"})
	if err := root.ExecuteContext(ctx); err == nil {
		t.Fatal("version accepted an operand")
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("query wrote state: %v, %v", entries, err)
	}
}

func TestVersionExecutableNeedsNoGoOrGit(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("builds the CLI")
	}
	bin := filepath.Join(t.TempDir(), "pew")
	if out, err := exec.CommandContext(t.Context(), "go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	dir := t.TempDir()
	for _, args := range [][]string{{"version"}, {"--version"}, {"--help"}} {
		cmd := exec.CommandContext(t.Context(), bin, args...)
		cmd.Dir = dir
		cmd.Env = gotool.SetEnv(os.Environ(), "PATH", "")
		out, err := cmd.CombinedOutput()
		if err != nil || !strings.Contains(string(out), "version") && !strings.HasPrefix(string(out), "pew ") {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
	}
	if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
		t.Fatalf("version wrote project state: %v, %v", entries, err)
	}
}
