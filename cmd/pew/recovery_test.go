package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh"

	"github.com/greatliontech/pew/internal/compare"
	"github.com/greatliontech/pew/internal/run"
	"github.com/greatliontech/pew/internal/store"
)

type statusBrokenWriter struct{ err error }

func (w statusBrokenWriter) Write([]byte) (int, error) { return 0, w.err }

type statusExplanationWriter struct {
	first bytes.Buffer
	calls int
	err   error
}

func (w *statusExplanationWriter) Write(p []byte) (int, error) {
	w.calls++
	if w.calls == 1 {
		return w.first.Write(p)
	}
	return 0, w.err
}

func TestStatusExplanationFailureCannotReportSuccess(t *testing.T) {
	if testing.Short() {
		t.Skip("loads a fixture package")
	}
	dir := twoArmFixture(t)
	writeFile(t, filepath.Join(dir, "bench_test.go"), "package arms\nimport \"testing\"\nfunc BenchmarkFirst(b *testing.B) {}\n")
	withWorkingDir(t, dir)
	writeStatRecording(t, store.New(filepath.Join(dir, "benchmarks")), "", "BenchmarkFirst", 100)
	cause := errors.New("explanation output failed")
	w := &statusExplanationWriter{err: cause}
	if err := runStatus(t.Context(), w, "", "", false, true, false, []string{"."}); !errors.Is(err, cause) {
		t.Fatalf("lost explanation failure: %v", err)
	}
	if w.calls < 2 || !strings.Contains(w.first.String(), "stale") || !strings.Contains(w.first.String(), "BenchmarkFirst") {
		t.Fatalf("failure did not follow a successful verdict: %d, %s", w.calls, &w.first)
	}
	// The fixture's opaque manifest is deliberately not decodable. A known
	// stale verdict remains visible, but an incomplete requested explanation
	// is not a successful complete status report either.
	var out bytes.Buffer
	if err := runStatus(t.Context(), &out, "", "", false, true, false, []string{"."}); err == nil || !strings.Contains(err.Error(), "decode manifest") || !strings.Contains(out.String(), "BenchmarkFirst") {
		t.Fatalf("incomplete explanation reported success: %v\n%s", err, &out)
	}
}

func TestStatusPreservesInventoryWhenEnginePreparationFails(t *testing.T) {
	if testing.Short() {
		t.Skip("loads a fixture package")
	}
	dir := twoArmFixture(t)
	writeFile(t, filepath.Join(dir, "third_test.go"), "package arms\nimport \"testing\"\nfunc BenchmarkThird(b *testing.B) {}\n")
	withWorkingDir(t, dir)
	unusableRecordings(t, dir)
	writeStatRecording(t, store.New(filepath.Join(dir, "benchmarks")), "", "BenchmarkFirst", 100)
	t.Setenv("GOFLAGS", "-pgo="+filepath.Join(dir, "missing.pgo"))
	var out bytes.Buffer
	err := runStatus(t.Context(), &out, "", "", false, false, true, []string{"."})
	if err == nil || !strings.Contains(err.Error(), "missing.pgo") {
		t.Fatalf("preparation failure missing: %v\n%s", err, &out)
	}
	rows := map[string]statusJSONRow{}
	dec := json.NewDecoder(&out)
	for dec.More() {
		var row statusJSONRow
		if err := dec.Decode(&row); err != nil {
			t.Fatal(err)
		}
		rows[row.Benchmark] = row
	}
	if len(rows) != 3 || rows["BenchmarkFirst"].Error == "" || rows["BenchmarkSecond"].Reason != "format" || rows["BenchmarkThird"].Verdict != "unrecorded" {
		t.Fatalf("independent inventory hidden: %+v", rows)
	}
}

func TestStatusProvenanceRefusalPrecedesEveryVerdict(t *testing.T) {
	if testing.Short() {
		t.Skip("lists fixture modules")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.work"), "go 1.27.0\nuse (\n ./one\n ./two\n)\n")
	for _, name := range []string{"one", "two"} {
		writeFile(t, filepath.Join(dir, name, "go.mod"), "module example.com/"+name+"\n\ngo 1.26.4\n")
		writeFile(t, filepath.Join(dir, name, "bench_test.go"), "package bench\nimport \"testing\"\nfunc BenchmarkProbe(b *testing.B) {}\n")
	}
	t.Setenv("GOWORK", filepath.Join(dir, "go.work"))
	withWorkingDir(t, dir)
	prior := goVersionSampler
	t.Cleanup(func() { goVersionSampler = prior })
	goVersionSampler = func(_ context.Context, moduleDir string, _ testEnvironmentValue) (string, error) {
		if filepath.Base(moduleDir) == "two" {
			return "go99.1.0", nil
		}
		return runtime.Version(), nil
	}
	var out bytes.Buffer
	err := runStatus(t.Context(), &out, "", "", false, false, true, []string{"./one/...", "./two/..."})
	var refused *toolchainProvenanceError
	if !errors.As(err, &refused) || out.Len() != 0 {
		t.Fatalf("provenance refusal followed verdicts: %v\n%s", err, &out)
	}
}

func TestStatusReportsIndependentRowsAndFailsIncompleteReports(t *testing.T) {
	if testing.Short() {
		t.Skip("loads fixture packages")
	}
	for _, failure := range []string{"read", "analysis"} {
		t.Run(failure, func(t *testing.T) {
			dir := twoArmFixture(t)
			withWorkingDir(t, dir)
			unusableRecordings(t, dir)
			st := store.New(filepath.Join(dir, "benchmarks"))
			if failure == "read" {
				path := filepath.Join(st.Root, "BenchmarkFirst.txt")
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(t.TempDir(), "unreadable"), path); err != nil {
					t.Skipf("symlink fixture unavailable: %v", err)
				}
			} else {
				writeStatRecording(t, st, "", "BenchmarkFirst", 100)
				prior := newViewFor
				t.Cleanup(func() { newViewFor = prior })
				newViewFor = func(*gofresh.Engine, context.Context, []gofresh.Subject, string, gofresh.Kind) (*gofresh.View, error) {
					return nil, errors.New("analysis unavailable")
				}
			}
			for _, bench := range []string{"BenchmarkFirst", "BenchmarkSecond"} {
				if err := os.Rename(filepath.Join(st.Root, bench+".txt"), filepath.Join(st.Root, bench+".variant.txt")); err != nil {
					t.Fatal(err)
				}
			}
			for _, jsonOut := range []bool{false, true} {
				var out bytes.Buffer
				err := runStatus(t.Context(), &out, "", "variant", true, false, jsonOut, []string{"."})
				if err == nil || !strings.Contains(err.Error(), "incomplete report") {
					t.Fatalf("incomplete status passed: %v\n%s", err, &out)
				}
				if !strings.Contains(out.String(), "BenchmarkFirst") || !strings.Contains(out.String(), "BenchmarkSecond") {
					t.Fatalf("lost an independent row: %s", &out)
				}
				if jsonOut {
					dec := json.NewDecoder(&out)
					rows := map[string]statusJSONRow{}
					for dec.More() {
						var row statusJSONRow
						if err := dec.Decode(&row); err != nil {
							t.Fatal(err)
						}
						rows[row.Benchmark] = row
					}
					if len(rows) != 2 || rows["BenchmarkFirst"].Error == "" || rows["BenchmarkFirst"].Label != "variant" || rows["BenchmarkSecond"].Verdict != "stale" || rows["BenchmarkSecond"].Reason != "format" {
						t.Fatalf("incomplete coverage: %+v", rows)
					}
				} else if !strings.Contains(out.String(), "BenchmarkFirst.variant") {
					t.Fatalf("failed recording lost its variant: %s", &out)
				}
			}
			cause := errors.New("status output unavailable")
			if err := runStatus(t.Context(), statusBrokenWriter{cause}, "", "variant", false, false, false, []string{"."}); !errors.Is(err, cause) {
				t.Fatalf("failed status output reported success: %v", err)
			}
		})
	}
}

func unusableRecordings(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	old := map[string][]byte{
		"BenchmarkFirst":  []byte("pew-format: 2\npew-test-variant-ledger: " + strings.Repeat("A", bufio.MaxScanTokenSize+1) + "\nBenchmarkFirst-8 1 42 ns/op\n"),
		"BenchmarkSecond": []byte("pew-format: 3\nBenchmarkSecond-8 invalid-count 42 ns/op\n"),
	}
	for bench, data := range old {
		writeFile(t, filepath.Join(dir, "benchmarks", bench+".txt"), string(data))
	}
	return old
}

func TestRunRegeneratesUnusableRecordingsWithoutDeletingFirst(t *testing.T) {
	if testing.Short() {
		t.Skip("loads fixture packages")
	}
	for _, outcome := range []string{"success", "failure", "interruption"} {
		t.Run(outcome, func(t *testing.T) {
			dir := twoArmFixture(t)
			withWorkingDir(t, dir)
			old := unusableRecordings(t, dir)
			var out bytes.Buffer
			if err := runStatus(t.Context(), &out, "", "", false, false, true, []string{"."}); err != nil {
				t.Fatalf("status could not inventory known format failures: %v\n%s", err, &out)
			}
			if strings.Count(out.String(), `"verdict":"stale"`) != 2 || strings.Count(out.String(), `"reason":"format"`) != 2 {
				t.Fatalf("format failures disappeared from status: %s", &out)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			stub := armStub(nil)
			rc := runConfig{opts: run.Options{Count: 1, Benchtime: "1x", Bench: "."}, execute: func(dir, pin string, env, args []string) ([]byte, error) {
				for _, arg := range args {
					if arg == "-c" {
						return nil, nil
					}
				}
				if outcome == "interruption" {
					cancel()
					return nil, ctx.Err()
				}
				if outcome == "failure" {
					return nil, errors.New("measurement failed")
				}
				data, err := stub(dir, pin, env, args)
				return []byte(strings.NewReplacer("goos: linux", "goos: "+runtime.GOOS, "goarch: amd64", "goarch: "+runtime.GOARCH).Replace(string(data))), err
			}}
			out.Reset()
			err := runRun(ctx, &out, &bytes.Buffer{}, rc, []string{"."})
			if (err == nil) != (outcome == "success") {
				t.Fatalf("%s: run error %v\n%s", outcome, err, &out)
			}
			for bench, before := range old {
				data, err := os.ReadFile(filepath.Join(dir, "benchmarks", bench+".txt"))
				if err != nil {
					t.Fatal(err)
				}
				if outcome != "success" {
					if !bytes.Equal(data, before) {
						t.Fatalf("%s replaced %s without a successful measurement", outcome, bench)
					}
					continue
				}
				rows, err := store.Parse(bytes.NewReader(data), bench)
				if err != nil || !store.IsRecording(rows) || bytes.Equal(data, before) {
					t.Fatalf("%s was not regenerated: %v", bench, err)
				}
			}
		})
	}
}

func TestStatInventoriesUnusableHistoricalRecordingsAsFormatFailures(t *testing.T) {
	if testing.Short() {
		t.Skip("loads fixture packages")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/recovery\n\ngo 1.26.4\n")
	writeFile(t, filepath.Join(dir, "bench_test.go"), "package recovery\nimport \"testing\"\nfunc BenchmarkFirst(b *testing.B) {}\nfunc BenchmarkSecond(b *testing.B) {}\n")
	old := unusableRecordings(t, dir)
	commitFixture(t, dir)
	withWorkingDir(t, dir)
	var out, errout bytes.Buffer
	err := runStat(t.Context(), &out, &errout, statConfig{opts: compare.DefaultOptions(), failOnRegression: true}, nil)
	var empty *nothingComparedError
	if !errors.As(err, &empty) || !strings.Contains(errout.String(), "BenchmarkFirst") || !strings.Contains(errout.String(), "BenchmarkSecond") || !strings.Contains(out.String()+errout.String()+err.Error(), "format") {
		t.Fatalf("legacy inventory not reported as format failures: %v\n%s\n%s", err, &out, &errout)
	}
	for bench, before := range old {
		data, err := os.ReadFile(filepath.Join(dir, "benchmarks", bench+".txt"))
		if err != nil || !bytes.Equal(data, before) {
			t.Fatalf("historical comparison changed %s: %v", bench, err)
		}
	}
}
