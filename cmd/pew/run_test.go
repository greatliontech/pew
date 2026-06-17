package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	runpkg "github.com/thegrumpylion/pew/internal/run"
)

func TestRunRunReturnsErrorOnPackageFailure(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/runfail\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "p_test.go"), []byte("package runfail\n\nimport \"testing\"\n\nfunc BenchmarkX(b *testing.B) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)

	var out, errOut bytes.Buffer
	err := runRun(&out, &errOut, runConfig{opts: runpkg.Options{Count: 1, Benchtime: "1x", Bench: "."}}, []string{"./..."})
	if err == nil {
		t.Fatal("runRun succeeded despite a per-package failure")
	}
	if !strings.Contains(out.String(), "error") {
		t.Fatalf("output = %q, want package error row", out.String())
	}
}
