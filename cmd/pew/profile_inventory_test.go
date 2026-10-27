package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	git "github.com/go-git/go-git/v5"
	"github.com/greatliontech/pew/internal/compare"
	"github.com/greatliontech/pew/internal/run"
)

func TestProfileRequestIncludesBenchmarksUnrecordedOnBothSides(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("produces a real measurement and diagnostic beside an unrecorded declaration")
	}
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/profileinventory\n\ngo 1.27.0\n")
	writeFile(t, filepath.Join(root, "bench_test.go"), `package profileinventory
import "testing"
var sink []byte
//gofresh:pure
func BenchmarkRecorded(b *testing.B) { for b.Loop() { sink = make([]byte, 2<<20) } }
func BenchmarkMissing(b *testing.B) {}
`)
	commitFixture(t, root)
	ctx := withWorkingDir(t, root)
	var output, diagnostics bytes.Buffer
	rc := runConfig{opts: run.Options{Bench: "^BenchmarkRecorded$", Count: 2, Benchtime: "1x"}, profile: "alloc", profileBenchtime: "32x"}
	if err := runRun(ctx, &output, &diagnostics, rc, []string{"."}); err != nil {
		t.Fatalf("produce positive diagnostic: %v\n%s\n%s", err, &output, &diagnostics)
	}
	repo, err := git.PlainOpen(root)
	if err != nil {
		t.Fatal(err)
	}
	ref := commitAll(t, repo, "recorded sibling").String()
	for _, jsonOut := range []bool{false, true} {
		output.Reset()
		diagnostics.Reset()
		err := runStat(ctx, &output, &diagnostics, statConfig{profile: "alloc", jsonOut: jsonOut, opts: compare.DefaultOptions()}, []string{ref})
		if err == nil || exitCode(err) != 2 {
			t.Fatalf("json=%t: absent requested diagnostic disappeared: %v\n%s\n%s", jsonOut, err, &output, &diagnostics)
		}
		if jsonOut {
			reports, statistics := decodeProfileReport(t, output.Bytes())
			if len(reports) != 2 || statistics == 0 {
				t.Fatalf("requested population or statistical sibling lost: %s", &output)
			}
			for _, report := range reports {
				switch report.Benchmark {
				case "BenchmarkRecorded":
					if !report.Compared {
						t.Fatalf("positive sibling not fulfilled: %+v", report)
					}
				case "BenchmarkMissing":
					if report.Compared || report.Base.Integrity != "unavailable" || report.New.Integrity != "unavailable" || report.Base.Reason == "" || report.New.Reason == "" {
						t.Fatalf("missing-both disposition: %+v", report)
					}
				default:
					t.Fatalf("unrequested report: %+v", report)
				}
			}
		} else if !strings.Contains(output.String(), "profile example.com/profileinventory.BenchmarkMissing alloc: compared=false") || !strings.Contains(output.String(), "profile example.com/profileinventory.BenchmarkRecorded alloc: compared=true") {
			t.Fatalf("text lost independent requested outcomes: %s", &output)
		}
	}
	// Historical comparisons promise only the union of historical recordings;
	// today's unrecorded declaration must not widen that separate population.
	output.Reset()
	if err := runStat(ctx, &output, &diagnostics, statConfig{profile: "alloc", jsonOut: true, opts: compare.DefaultOptions()}, []string{ref, ref}); err != nil {
		t.Fatalf("historical recorded-only request: %v\n%s", err, &output)
	}
	reports, _ := decodeProfileReport(t, output.Bytes())
	if len(reports) != 1 || !reports[0].Compared || reports[0].Benchmark != "BenchmarkRecorded" {
		t.Fatalf("current missing declaration entered historical request: %+v", reports)
	}
	// Informational statistics without a profile request keep their own policy.
	output.Reset()
	if err := runStat(ctx, &output, &diagnostics, statConfig{jsonOut: true, opts: compare.DefaultOptions()}, []string{ref}); err != nil {
		t.Fatalf("unrequested profile changed statistical exit: %v", err)
	}
	if reports, _ := decodeProfileReport(t, output.Bytes()); len(reports) != 0 {
		t.Fatalf("invented profile obligations: %+v", reports)
	}
}
