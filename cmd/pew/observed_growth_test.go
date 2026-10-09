package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	gogit "github.com/go-git/go-git/v5"
	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/pew/internal/compare"
	runpkg "github.com/greatliontech/pew/internal/run"
	"github.com/greatliontech/pew/internal/store"
)

func TestObservedMeasurementRetainsProducingSupportAcrossGrowth(t *testing.T) {
	if testing.Short() {
		t.Skip("executes measured subjects and checks repeated applicability")
	}
	if runtime.GOOS != "linux" {
		t.Skip("immutable-environment method is Linux-audited")
	}
	root := twoArmFixture(t)
	writeFile(t, filepath.Join(root, "bench_test.go"), `package arms
import ("os"; "testing")
func BenchmarkFirst(b *testing.B) { for i:=0;i<b.N;i++ { _=os.Getenv("PEW_OBSERVED_VALUE") } }
func BenchmarkSecond(b *testing.B) { _,_=os.ReadFile("fixture.txt") }
`)
	writeFile(t, filepath.Join(root, "fixture.txt"), "stable file")
	t.Setenv("PEW_OBSERVED_VALUE", "before")
	withWorkingDir(t, root)
	st := store.New(filepath.Join(root, "benchmarks"))
	repo, err := gogit.PlainOpen(root)
	if err != nil {
		t.Fatal(err)
	}
	commitAll(t, repo, "measured subjects")
	var log bytes.Buffer
	if err := runRun(context.Background(), io.Discard, &log, runConfig{all: true, benchDir: st.Root, opts: runpkg.Options{Bench: ".", Count: 1, Benchtime: "1x"}}, []string{"."}); err != nil {
		t.Fatalf("run: %v\n%s", err, &log)
	}
	read := func() admission {
		t.Helper()
		rows, err := st.Read("", "BenchmarkFirst", "")
		if err != nil {
			t.Fatal(err)
		}
		a := admitRecording(rows, true)
		if !a.ok {
			t.Fatalf("admission: %+v", a)
		}
		return a
	}
	original := read()
	commitAll(t, repo, "recordings")
	if !original.fp.ObservationProof.Observable || original.fp.ObservationAssertion == "" {
		t.Fatal("missing producing proof")
	}
	subject := gofresh.Subject{Package: "example.com/arms", Symbol: "BenchmarkFirst"}
	engine := func() *gofresh.Engine {
		t.Helper()
		e, _, err := newEngineAt(context.Background(), root, root, false, testEnvironment(t, os.Environ()))
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	judge := func(e *gofresh.Engine) map[string]*benchVerdict {
		t.Helper()
		rows, err := checkPackage(context.Background(), st, func(subjects []gofresh.Subject) (*gofresh.View, error) {
			return e.NewViewFor(context.Background(), subjects, root, gofresh.Measurement)
		}, subject.Package, "", root, []string{"BenchmarkFirst", "BenchmarkSecond"}, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range rows {
			if row.err != nil {
				t.Fatal(row.err)
			}
		}
		return rows
	}
	e := engine()
	ordinary, err := e.Check(context.Background(), original.fp, subject, root)
	if err != nil || ordinary.Status != gofresh.Unverifiable {
		t.Fatalf("ordinary=%+v %v; sibling file dependence must remain", ordinary, err)
	}
	rows := judge(e)
	if rows[subject.Symbol].v != verdictValid || rows["BenchmarkSecond"].v != verdictUnverifiable {
		t.Fatalf("observed verdicts: first=%+v second=%+v", rows[subject.Symbol], rows["BenchmarkSecond"])
	}
	var statusOut bytes.Buffer
	if err := runStatus(t.Context(), &statusOut, st.Root, "", false, false, true, []string{"."}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(statusOut.String(), `"benchmark":"BenchmarkFirst","verdict":"valid"`) || !strings.Contains(statusOut.String(), `"benchmark":"BenchmarkSecond","verdict":"unverifiable"`) {
		t.Fatalf("status disagrees: %s", &statusOut)
	}
	var served bytes.Buffer
	if err := runRun(t.Context(), &served, io.Discard, runConfig{benchDir: st.Root, opts: runpkg.Options{Bench: "^BenchmarkFirst$", Count: 1, Benchtime: "1x"}, execute: func(string, string, []string, []string) ([]byte, error) {
		t.Fatal("valid observed arm was remeasured")
		return nil, nil
	}}, []string{"."}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(served.String(), "1 valid, nothing to run") {
		t.Fatalf("run did not serve: %s", &served)
	}
	statCtx, deps := testDependencies(t)
	loads := 0
	load := deps.view
	deps.view = func(e *gofresh.Engine, ctx context.Context, subjects []gofresh.Subject, dir string, kind gofresh.Kind) (*gofresh.View, error) {
		loads++
		return load(e, ctx, subjects, dir, kind)
	}
	var statOut, statErr bytes.Buffer
	if err := runStat(statCtx, &statOut, &statErr, statConfig{benchDir: st.Root, opts: compare.DefaultOptions()}, nil); err != nil {
		t.Fatal(err)
	}
	if loads != 1 || strings.Contains(statErr.String(), "BenchmarkFirst is") || !strings.Contains(statErr.String(), "BenchmarkSecond is unverifiable") {
		t.Fatalf("stat parity or batching: loads=%d\n%s", loads, &statErr)
	}
	if read().fp != original.fp {
		t.Fatal("read-only judgments changed historical evidence")
	}
	for generation := 1; generation <= 2; generation++ {
		writeFile(t, filepath.Join(root, fmt.Sprintf("extra%d_test.go", generation)), fmt.Sprintf("package arms\nfunc Uncalled%d() int { return %d }\n", generation, generation))
		e = engine()
		row := judge(e)[subject.Symbol]
		if row.v != verdictValid || row.grownLedger == "" {
			t.Fatalf("extension %d: %+v", generation, row)
		}
		if err := row.view.Validate(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := publishRefresh(st, "", subject.Symbol, "", row.admitted, row.fp, row.grownLedger); err != nil {
			t.Fatal(err)
		}
		back := read()
		producing := back.fp
		producing.InertTestVariantApplicability = gofresh.InertTestVariantApplicability{}
		if producing != original.fp {
			t.Fatalf("producing evidence changed:\n%+v\n%+v", producing, original.fp)
		}
		if back.fp.EffectiveTestVariantClosure() == original.fp.TestVariantClosure {
			t.Fatal("applicability did not advance")
		}
		for i := range original.rows {
			if !reflect.DeepEqual(original.rows[i].Values, back.rows[i].Values) {
				t.Fatal("measurement changed")
			}
		}
		if restarted := judge(engine())[subject.Symbol]; restarted.v != verdictValid || restarted.grownLedger != "" {
			t.Fatalf("restart: %+v", restarted)
		}
	}
	for _, mutate := range []struct {
		name  string
		apply func(*gofresh.Fingerprint)
	}{
		{"missing proof", func(f *gofresh.Fingerprint) {
			f.ObservationAssertion = ""
			f.ObservationProof = gofresh.ObservationProof{}
		}},
		{"wrong subject", func(f *gofresh.Fingerprint) { f.ObservationProof.Subject.Symbol = "BenchmarkSecond" }},
		{"broken integrity", func(f *gofresh.Fingerprint) { f.ObservationProof.Evidence = "corrupt" }},
		{"unknown applicability", func(f *gofresh.Fingerprint) { f.InertTestVariantApplicability.Strategy = "unknown" }},
	} {
		t.Run(mutate.name, func(t *testing.T) {
			f := read().fp
			mutate.apply(&f)
			v, err := engine().CheckObserved(context.Background(), f, subject, root)
			if err != nil {
				t.Fatal(err)
			}
			if v.Status == gofresh.Valid {
				t.Fatalf("unsupported record served: %+v", f)
			}
		})
	}
	t.Setenv("PEW_OBSERVED_VALUE", "after")
	if moved := judge(engine())[subject.Symbol]; moved.v != verdictStale || !strings.Contains(moved.reason, "PEW_OBSERVED_VALUE") {
		t.Fatalf("environment movement: %s %q", moved.v, moved.reason)
	}
}
