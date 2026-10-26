package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/greatliontech/pew/internal/gotool"
	"github.com/greatliontech/pew/internal/profiles"
	runpkg "github.com/greatliontech/pew/internal/run"
	"github.com/greatliontech/pew/internal/store"
)

func TestProfileExecutableSurvivesCaptureAndRemainsGuarded(t *testing.T) {
	if testing.Short() {
		t.Skip("executes a real CPU-profile capture and subsequent status")
	}
	root := twoArmFixture(t)
	writeFile(t, filepath.Join(root, "bench_test.go"), "package arms\nimport \"testing\"\nvar sink uint64\nfunc BenchmarkFirst(b *testing.B) { for b.Loop() { sink++ } }\nfunc BenchmarkSecond(b *testing.B) {}\n")
	withWorkingDir(t, root)
	benchDir := filepath.Join(t.TempDir(), "benchmarks")
	rc := runConfig{benchDir: benchDir, profile: "cpu", profileBenchtime: "300ms", opts: runpkg.Options{Count: 1, Benchtime: "1x", Bench: "^BenchmarkFirst$"}}
	var executable string
	rc.diagnostic = func(ctx context.Context, dir, pin string, env gotool.Environment, bin string, args []string, w io.Writer) ([]byte, error) {
		executable = bin
		return runpkg.ExecuteDiagnostic(ctx, dir, pin, env, bin, args, w)
	}
	var out, diagnostics bytes.Buffer
	if err := runRun(t.Context(), &out, &diagnostics, rc, []string{"."}); err != nil {
		t.Fatalf("run: %v\n%s\n%s", err, &out, &diagnostics)
	}
	data, err := os.ReadFile(executable)
	if err != nil {
		t.Fatalf("capture discarded its executable dependency: %v", err)
	}
	st := store.New(benchDir)
	rows, err := st.Read("", "BenchmarkFirst", "")
	if err != nil {
		t.Fatal(err)
	}
	index, present, err := profiles.FromRows(rows)
	if err != nil || !present || len(index.Captures) != 1 || index.Captures[0].BinarySHA256 != profiles.Digest(data) {
		t.Fatalf("executable identity lost: %+v %v", index, err)
	}
	status := func() statusJSONRow {
		t.Helper()
		out.Reset()
		if err := runStatus(t.Context(), &out, benchDir, "", false, false, true, []string{"."}); err != nil {
			t.Fatal(err)
		}
		decoder := json.NewDecoder(&out)
		for decoder.More() {
			var row statusJSONRow
			if err := decoder.Decode(&row); err != nil {
				t.Fatal(err)
			}
			if row.Benchmark == "BenchmarkFirst" {
				return row
			}
		}
		t.Fatal("missing measured row")
		return statusJSONRow{}
	}
	row := status()
	if row.Verdict != "valid" || len(row.Profiles) != 1 || row.Profiles[0].Freshness == "stale" {
		t.Fatalf("unchanged fixed producer/checker: %+v", row)
	}
	// An unobserved build-determining variable still belongs to the compiler's
	// build environment. Identical executable identities alone do not make two
	// compilation environments compatible, even when a benchmark does not read the variable.
	t.Run("changed-build-environment", func(t *testing.T) {
		t.Setenv("CGO_CFLAGS", "-DPEW_BUILD_ENVIRONMENT_PROBE=different_compiler_input")
		row := status()
		if row.Verdict != "stale" || row.Reason != "buildconfig" || len(row.Profiles) != 1 || row.Profiles[0].Freshness != "stale" || !strings.Contains(row.Profiles[0].Reason, "buildconfig") {
			t.Fatalf("different build environment unguarded: %+v", row)
		}
		var explained bytes.Buffer
		if err := runStatus(t.Context(), &explained, benchDir, "", false, true, false, []string{"."}); err != nil {
			t.Fatalf("status --explain: %v", err)
		}
		text := explained.String()
		if !strings.Contains(text, "buildconfig: build description differed between capture and check (compilation environment and build inputs; values are not disclosed)") || !strings.Contains(text, "recorded observation:") || !strings.Contains(text, "outcome=identity-only") {
			t.Fatalf("status --explain missing buildconfig/observation/profile attribution:\n%s", text)
		}
		if strings.Contains(text, "different_compiler_input") {
			t.Fatalf("status --explain disclosed environment value:\n%s", text)
		}
	})
	// CPU profiling reads the retained executable. Its continued existence is
	// not an exemption: a later content edit must move the recorded dependency.
	if err := os.Chmod(executable, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, append(data, 0), 0o700); err != nil {
		t.Fatal(err)
	}
	row = status()
	if len(row.Profiles) != 1 || row.Profiles[0].Freshness != "stale" || !strings.Contains(row.Profiles[0].Reason, executable) {
		t.Fatalf("changed executable unguarded: %+v", row)
	}
}
