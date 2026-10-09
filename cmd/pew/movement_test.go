package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/runtimeinput"
	"github.com/greatliontech/pew/internal/recordingtest"
	runpkg "github.com/greatliontech/pew/internal/run"
	"github.com/greatliontech/pew/internal/store"
)

func TestNoncallAndMutableLocalMovementStalesStoredMeasurement(t *testing.T) {
	if testing.Short() {
		t.Skip("loads source-movement fixture modules")
	}
	for _, tc := range []struct {
		name, source, path, replacement string
		extra                           map[string]string
	}{
		{"constant", "package movement\nconst Size = 4096\nfunc Value() int { return Size }\n", "value.go", "package movement\nconst Size = 8192\nfunc Value() int { return Size }\n", nil},
		{"struct", "package movement\nimport \"unsafe\"\ntype T struct { X byte }\nfunc Value() int { return int(unsafe.Sizeof(T{})) }\n", "value.go", "package movement\nimport \"unsafe\"\ntype T struct { X byte; Y uint64 }\nfunc Value() int { return int(unsafe.Sizeof(T{})) }\n", nil},
		{"embed", "package movement\nimport _ \"embed\"\n//go:embed data.txt\nvar data string\nfunc Value() int { return len(data) }\n", "data.txt", "longer embedded input", map[string]string{"data.txt": "short"}},
		{"replace", "package movement\nimport \"example.com/dep\"\nfunc Value() int { return dep.Value() }\n", "dep/value.go", "package dep\nfunc Value() int { return 2 }\n", map[string]string{"dep/go.mod": "module example.com/dep\n\ngo 1.26\n", "dep/value.go": "package dep\nfunc Value() int { return 1 }\n"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			mod := "module example.com/movement\n\ngo 1.26\n"
			if tc.name == "replace" {
				mod += "require example.com/dep v0.0.0\nreplace example.com/dep => ./dep\n"
			}
			writeFile(t, filepath.Join(root, "go.mod"), mod)
			writeFile(t, filepath.Join(root, "value.go"), tc.source)
			writeFile(t, filepath.Join(root, "bench_test.go"), "package movement\nimport \"testing\"\n//gofresh:pure\nfunc BenchmarkValue(b *testing.B) { for i:=0;i<b.N;i++ { _=Value() } }\n")
			for name, text := range tc.extra {
				if err := os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0755); err != nil {
					t.Fatal(err)
				}
				writeFile(t, filepath.Join(root, name), text)
			}
			env := testEnvironment(t, os.Environ())
			engine, _, err := newEngineAt(context.Background(), root, root, false, env)
			if err != nil {
				t.Fatal(err)
			}
			subject := gofresh.Subject{Package: "example.com/movement", Symbol: "BenchmarkValue"}
			view, err := engine.NewViewFor(context.Background(), []gofresh.Subject{subject}, root, gofresh.Measurement)
			if err != nil {
				t.Fatal(err)
			}
			fp, err := view.Capture(context.Background(), subject)
			if err != nil {
				t.Fatal(err)
			}
			ledger, err := view.TestVariantLedger(subject)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := runpkg.EncodeLedger(runpkg.LedgerFromGofresh(ledger))
			if err != nil {
				t.Fatal(err)
			}
			rt, err := runtimeinput.Incomplete(root, "fixture", "no producing observation", env.Values())
			if err != nil {
				t.Fatal(err)
			}
			st := store.New(t.TempDir())
			if err := st.Write("", subject.Symbol, "", recordingtest.Results("Value", []float64{1}, recordingtest.Measured(fp, encoded, rt.Digest, rt.Manifest))); err != nil {
				t.Fatal(err)
			}
			judge := func() *benchVerdict {
				t.Helper()
				rows, err := checkPackage(context.Background(), st, func(subjects []gofresh.Subject) (*gofresh.View, error) {
					return engine.NewViewFor(context.Background(), subjects, root, gofresh.Measurement)
				}, subject.Package, "", root, []string{subject.Symbol}, "", nil)
				if err != nil || rows[subject.Symbol].err != nil {
					t.Fatalf("judgment: %v %+v", err, rows)
				}
				return rows[subject.Symbol]
			}
			if baseline := judge(); baseline.v != verdictValid {
				t.Fatalf("baseline: %+v", baseline)
			}
			stored, err := st.Read("", subject.Symbol, "")
			if err != nil {
				t.Fatal(err)
			}
			deferred, err := judgeRecordings(context.Background(), func(subjects []gofresh.Subject) (*gofresh.View, error) {
				view, err := engine.NewViewFor(context.Background(), subjects, root, gofresh.Measurement)
				if err == nil {
					writeFile(t, filepath.Join(root, tc.path), tc.replacement)
				}
				return view, err
			}, subject.Package, []string{subject.Symbol}, map[string]*benchVerdict{subject.Symbol: {admitted: admitRecording(stored, true)}}, nil)
			if err != nil || !errors.Is(deferred[subject.Symbol].err, gofresh.ErrViewChanged) {
				t.Fatalf("provisional source judgment escaped validation: %v %+v", err, deferred[subject.Symbol])
			}
			moved := judge()
			if moved.v != verdictStale || moved.reason != "closure" {
				t.Fatalf("movement: %+v", moved)
			}
			current := moved.current
			if current.MaximalClosure == fp.MaximalClosure || current.Guards != fp.Guards {
				t.Fatalf("movement must change source alone: old=%+v new=%+v", fp, current)
			}
			gotMod, err := os.ReadFile(filepath.Join(root, "go.mod"))
			if err != nil || string(gotMod) != mod {
				t.Fatal("module selection changed")
			}
		})
	}
}
