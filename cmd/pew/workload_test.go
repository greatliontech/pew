package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"path/filepath"
	"strings"
	"testing"

	"github.com/greatliontech/pew/internal/run"
	"github.com/greatliontech/pew/internal/store"
	"golang.org/x/perf/benchfmt"
)

func TestWorkloadKeepsSelectionEffortAndSamplePopulation(t *testing.T) {
	want, err := requestedWorkload(run.Options{Bench: "^BenchmarkX$/^case-2$", Count: 2, Benchtime: "+01x"}, "BenchmarkX")
	if err != nil {
		t.Fatal(err)
	}
	if want.Budget != "1x" || want.Count != 2 {
		t.Fatalf("work account = %+v", want)
	}
	rows := []*benchfmt.Result{{Name: []byte("X/case-2")}, {Name: []byte("X/case-2")}}
	if !fulfillsWorkload(&want, want, rows) {
		t.Fatal("matching workload refused")
	}
	for _, change := range []func(*workload){
		func(w *workload) { w.Selection = "^BenchmarkX$/^case$" },
		func(w *workload) { w.Count = 1 },
		func(w *workload) { w.Budget = "2x" },
		func(w *workload) { w.Version = 2 },
	} {
		stored := want
		change(&stored)
		if fulfillsWorkload(&stored, want, rows) {
			t.Fatalf("different workload served: %+v", stored)
		}
	}
	if fulfillsWorkload(nil, want, rows) || fulfillsWorkload(&want, want, rows[:1]) {
		t.Fatal("missing work account or samples served")
	}
	more := want
	more.Count++
	if !fulfillsWorkload(&more, want, append(rows, rows[0])) {
		t.Fatal("greater recorded count failed to cover the request")
	}
	// Suffix interpretation never merges two emitted populations.
	if fulfillsWorkload(&want, want, []*benchfmt.Result{{Name: []byte("X/case-2")}, {Name: []byte("X/case-2-2")}}) {
		t.Fatal("distinct full row names jointly met one sample floor")
	}
}

func TestWorkloadAdmitsBothHarnessCPUSuffixRepresentations(t *testing.T) {
	for _, tt := range []struct {
		row, selected string
		ok            bool
	}{
		{"X/case-2", "case-2", true},
		{"X/case-2", "case", true},
		{"X/case-2-2", "case-2", true},
		{"X/case-2-2", "case", false},
		{"X/case-1", "case", false},
		{"X/case-1", "case-1", true},
		{"X/case-02", "case", false},
		{"X/case-+2", "case", false},
		{"X/other-2", "case", false},
		{"X/case-2/tail-4", "case-2$/^tail", true},
	} {
		t.Run(tt.row+"/"+tt.selected, func(t *testing.T) {
			w := workload{Version: 1, Count: 1, Budget: "1x", Selection: "^BenchmarkX$/^" + tt.selected + "$"}
			err := workloadAcceptsRows(w, []*benchfmt.Result{{Name: []byte(tt.row)}})
			if (err == nil) != tt.ok {
				t.Fatalf("row admission = %v, want accepted=%v", err, tt.ok)
			}
		})
	}
}

func TestWorkloadRecordRefusesMissingAndNoncanonicalAuthority(t *testing.T) {
	w := workload{Version: 1, Count: 2, Budget: "1x", Selection: "^BenchmarkX$"}
	encoded, err := encodeWorkload(w)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeWorkload(encoded)
	if err != nil || decoded == nil || *decoded != w {
		t.Fatalf("round trip: %+v, %v", decoded, err)
	}
	if got, err := decodeWorkload(""); got != nil || err != nil {
		t.Fatalf("absence became evidence: %+v, %v", got, err)
	}
	for _, body := range []string{
		`{"version":1,"selection":"^BenchmarkX$","count":2,"budget":"01x"}`,
		`{"version":1,"selection":"^BenchmarkX$","count":2,"count":2,"budget":"1x"}`,
		`{"version":1,"selection":"^BenchmarkX$","count":2,"budget":"1x","extra":0}`,
		`{"version":1,"selection":"^BenchmarkX$","count":2,"budget":"1x"}null`,
		`{"version":1,"selection":"[","count":2,"budget":"1x"}`,
		`{"version":1,"selection":"^BenchmarkX$","count":0,"budget":"1x"}`,
	} {
		if got, err := decodeWorkload(base64.RawURLEncoding.EncodeToString([]byte(body))); err == nil {
			t.Fatalf("invalid work account admitted: %s => %+v", body, got)
		}
	}
}

func TestRetainedArmServesOnlyItsRequestedChildAndEffort(t *testing.T) {
	if testing.Short() {
		t.Skip("executes and subsequently serves a child benchmark")
	}
	for _, width := range []string{"1", "2"} {
		t.Run(width, func(t *testing.T) {
			dir := t.TempDir()
			writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/workload\n\ngo 1.26\n")
			writeFile(t, filepath.Join(dir, "bench_test.go"), `package workload
import "testing"
//gofresh:pure
func BenchmarkX(b *testing.B) {
 children := map[bool][]string{false: {"unexpected"}, true: {"case", "case-2"}}[testing.Short()]
 for _, name := range children {
  b.Run(name, func(b *testing.B) { for b.Loop() {} })
 }
}
`)
			commitFixture(t, dir)
			withWorkingDir(t, dir)
			t.Setenv("GOMAXPROCS", width)
			t.Setenv("GOFLAGS", "-v=0 -short")
			measurements := 0
			rc := runConfig{benchDir: filepath.Join(dir, "benchmarks"), opts: run.Options{Count: 2, Benchtime: "1x", Bench: "^BenchmarkX$/^case-2$"}, binary: func(ctx context.Context, dir, pin string, env testEnvironmentValue, binary string, args []string) ([]byte, error) {
				measurements++
				return run.ExecuteBinaryContext(ctx, dir, pin, env, binary, args)
			}}
			for attempt, pattern := range []string{"^BenchmarkX$/^case-2$", "^BenchmarkX$/^case-2$", "^BenchmarkX$/^case$"} {
				rc.opts.Bench = pattern
				var out, errout bytes.Buffer
				if err := runRun(t.Context(), &out, &errout, rc, []string{"."}); err != nil {
					t.Fatalf("attempt %d: %v\n%s\n%s", attempt, err, &out, &errout)
				}
				want := []int{1, 1, 2}[attempt]
				if measurements != want {
					t.Fatalf("attempt %d measured %d, want %d: %s", attempt, measurements, want, &out)
				}
				if attempt == 1 && !strings.Contains(out.String(), "nothing to run") {
					t.Fatalf("served request unreported: %s", &out)
				}
			}
			rows, err := store.New(rc.benchDir).Read("", "BenchmarkX", "")
			if err != nil || len(rows) != 2 {
				t.Fatalf("selected sample population: %d rows, %v", len(rows), err)
			}
		})
	}
}
