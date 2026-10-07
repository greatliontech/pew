package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh"
	runpkg "github.com/greatliontech/pew/internal/run"
	"github.com/greatliontech/pew/internal/store"
)

func TestRunRecordsPerArmOutcomeSupport(t *testing.T) {
	if testing.Short() {
		t.Skip("executes benchmark processes and prepares outcome proofs")
	}
	if runtime.GOOS != "linux" {
		t.Skip("shared immutable-environment outcome method is Linux-audited")
	}
	dir := twoArmFixture(t)
	withWorkingDir(t, dir)
	benchDir := filepath.Join(t.TempDir(), "benchmarks")
	var out, errout bytes.Buffer
	if err := runRun(context.Background(), &out, &errout, runConfig{all: true, benchDir: benchDir, opts: runpkg.Options{Count: 1, Benchtime: "1x", Bench: "."}}, []string{"."}); err != nil {
		t.Fatalf("run: %v\n%s", err, &errout)
	}
	var previous string
	for _, name := range []string{"BenchmarkFirst", "BenchmarkSecond"} {
		recs, err := store.New(benchDir).Read("", name, "")
		if err != nil || len(recs) == 0 {
			t.Fatalf("record %s: %v", name, err)
		}
		fp, _, ok := fingerprintFromConfig(recs[0].Config)
		if !ok {
			t.Fatal("record does not admit")
		}
		if fp.ObservationProof != (gofresh.ObservationProof{}) {
			t.Fatal("construction proof enabled an unselected read policy")
		}
		data, err := base64.RawURLEncoding.DecodeString(fp.RuntimeInputs)
		if err != nil {
			t.Fatal(err)
		}
		var manifest struct {
			Version  int      `json:"v"`
			Outcome  string   `json:"outcome"`
			Subjects []string `json:"subjects"`
		}
		if err := json.Unmarshal(data, &manifest); err != nil {
			t.Fatal(err)
		}
		if manifest.Version != 2 || manifest.Outcome != "gofresh/immutable-environment@1" || len(manifest.Subjects) != 1 {
			t.Fatalf("%s support=%s", name, data)
		}
		if manifest.Subjects[0] == previous {
			t.Fatal("separate arms share a subject support identity")
		}
		previous = manifest.Subjects[0]
	}
}

func TestArmWriteGateRequiresOutcomeValidation(t *testing.T) {
	if testing.Short() {
		t.Skip("constructs an observed producer transaction")
	}
	dir := twoArmFixture(t)
	withWorkingDir(t, dir)
	p := pkgMeta{ImportPath: "example.com/arms", Name: "arms", Dir: dir, TestGoFiles: []string{"bench_test.go"}}
	p.Module.Path, p.Module.Dir = "example.com/arms", dir
	env := testEnvironment(t, os.Environ())
	engine, pgo, err := newEngineForPkg(context.Background(), p, env)
	if err != nil {
		t.Fatal(err)
	}
	subject := gofresh.Subject{Package: p.ImportPath, Symbol: "BenchmarkFirst"}
	parent, err := engine.NewViewFor(context.Background(), []gofresh.Subject{subject}, dir, gofresh.Measurement)
	if err != nil {
		t.Fatal(err)
	}
	fp, err := parent.Capture(context.Background(), subject)
	if err != nil {
		t.Fatal(err)
	}
	observed, err := parent.Sibling([]gofresh.Subject{subject})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := observed.CaptureObserved(context.Background(), subject); err != nil {
		t.Fatal(err)
	}
	prep := &packagePreparation{pkg: p, st: store.New(filepath.Join(t.TempDir(), "benchmarks")), pgoInput: pgo}
	err = persistArm(context.Background(), io.Discard, io.Discard, runConfig{}, newGitStateCache(nil), p, prep, parent, "", true, "", subject.Symbol, fp, armMeasurement{outcomeView: observed}, env)
	if err == nil || !strings.Contains(err.Error(), "no attached completed observation") {
		t.Fatalf("write gate bypassed outcome validation: %v", err)
	}
}
