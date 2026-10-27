package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/pew/internal/recordingtest"
	runpkg "github.com/greatliontech/pew/internal/run"
	"github.com/greatliontech/pew/internal/store"
)

func TestRunRecordsPerArmOutcomeSupport(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("executes benchmark processes and prepares outcome proofs")
	}
	if runtime.GOOS != "linux" {
		t.Skip("shared immutable-environment outcome method is Linux-audited")
	}
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/arms\n\ngo 1.26.4\n")
	writeFile(t, filepath.Join(dir, "bench_test.go"), "package arms\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\nfunc BenchmarkFirst(b *testing.B) { _ = os.Getenv(\"PEW_OUTCOME_SUPPORT\") }\nfunc BenchmarkSecond(b *testing.B) { _ = os.Getenv(\"PEW_OUTCOME_SUPPORT\") }\n")
	commitFixture(t, dir)
	ctx := withWorkingDir(t, dir)
	benchDir := filepath.Join(t.TempDir(), "benchmarks")
	var out, errout bytes.Buffer
	if err := runRun(ctx, &out, &errout, runConfig{all: true, benchDir: benchDir, opts: runpkg.Options{Count: 1, Benchtime: "1x", Bench: "."}}, []string{"."}); err != nil {
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
		if fp.ObservationProof.Subject.Symbol != name || !fp.ObservationProof.Observable || fp.ObservationAssertion == "" {
			t.Fatal("record lost the arm's selected observation proof")
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
		if manifest.Version != 4 || manifest.Outcome != "gofresh/immutable-environment@1" || len(manifest.Subjects) != 1 {
			t.Fatalf("%s support=%s", name, data)
		}
		if manifest.Subjects[0] == previous {
			t.Fatal("separate arms share a subject support identity")
		}
		previous = manifest.Subjects[0]
	}
}

func TestArmWriteGateRequiresOutcomeValidation(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("constructs an observed producer transaction")
	}
	dir := twoArmFixture(t)
	ctx := withWorkingDir(t, dir)
	p := pkgMeta{ImportPath: "example.com/arms", Name: "arms", Dir: dir, TestGoFiles: []string{"bench_test.go"}}
	p.Module.Path, p.Module.Dir = "example.com/arms", dir
	env := testEnvironment(t, os.Environ())
	engine, _, err := newEngineForPkg(ctx, p, env)
	if err != nil {
		t.Fatal(err)
	}
	subject := gofresh.Subject{Package: p.ImportPath, Symbol: "BenchmarkFirst"}
	parent, err := engine.bind(ctx, []gofresh.Subject{subject}, gofresh.Measurement)
	if err != nil {
		t.Fatal(err)
	}
	_, binding := fixtureArtifact(t, ctx, parent.Snapshot(), p.ImportPath)
	unit, err := parent.Begin(subject)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := unit.Register(ctx, binding, gofresh.ProcessDescription{Identity: "unsettled", Capture: filepath.Join(t.TempDir(), "testlog")}); err != nil {
		t.Fatal(err)
	}
	if result, err := unit.Close(ctx); !errors.Is(err, gofresh.ErrUnitIncomplete) || result.Fingerprint.MaximalClosure != "" {
		t.Fatalf("missing terminal/capture became final evidence: %v", err)
	}
	st := store.New(filepath.Join(t.TempDir(), "benchmarks"))
	if err := st.Write("", "BenchmarkSecond", "", recordingtest.Results("Second", []float64{999})); err != nil {
		t.Fatal(err)
	}
	key := store.Key{Bench: "BenchmarkSecond"}
	before, err := st.ReadBytes(key)
	if err != nil {
		t.Fatal(err)
	}
	rc := runConfig{all: true, benchDir: st.Root, opts: runpkg.Options{Count: 1, Benchtime: "1x", Bench: "."}, beforePersist: func(name string) {
		if name == "BenchmarkSecond" {
			writeFile(t, filepath.Join(dir, "after_test.go"), "package arms\nfunc AddedAfterExecution() {}\n")
		}
	}}
	var output bytes.Buffer
	if err := runRun(ctx, &output, io.Discard, rc, []string{"."}); err == nil || !strings.Contains(output.String(), "analysis view changed") {
		t.Fatalf("failed closing escaped publication: %v\n%s", err, &output)
	}
	after, err := st.ReadBytes(key)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("failed second closing replaced prior bytes: %v", err)
	}
	if _, err := st.ReadBytes(store.Key{Bench: "BenchmarkFirst"}); err != nil {
		t.Fatalf("successful first sibling was lost: %v", err)
	}
}
