package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/greatliontech/pew/internal/run"
	"github.com/greatliontech/pew/internal/store"
	"golang.org/x/perf/benchfmt"
)

// abFixtureRepo builds a one-commit git repository holding a module with
// one benchmark package, and returns the module dir (== repo root).
func abFixtureRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":      "module example.com/abfix\n\ngo 1.24\n",
		"p/p.go":      "package p\n\nfunc Work(n int) int {\n\ts := 0\n\tfor i := 0; i < n; i++ {\n\t\ts += i\n\t}\n\treturn s\n}\n",
		"p/p_test.go": "package p\n\nimport \"testing\"\n\nfunc BenchmarkWork(b *testing.B) {\n\tfor i := 0; i < b.N; i++ {\n\t\tWork(100)\n\t}\n}\n",
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{
		{"init", "-q"}, {"add", "-A"},
		{"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "base"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	return dir
}

// The A/B mode's structural guarantees, all through the seams: both
// sides build before either side measures, execution interleaves A/B
// per iteration (block ordering folds machine drift into the delta),
// each side runs from its own tree, and the report names the sides.
func TestABInterleavesAfterBothSidesBuild(t *testing.T) {
	dir := abFixtureRepo(t)
	prior, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(filepath.Join(dir, "p")); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(prior)

	var log []string
	ac := abConfig{
		bench: ".", count: 3, ref: "HEAD",
		throttle: func() run.ThrottleSnapshot { return run.ThrottleSnapshot{} },
		build: func(buildDir string, env []string, args []string) error {
			log = append(log, "build")
			return nil
		},
		execute: func(execDir, pin string, env []string, bin string, args []string) ([]byte, error) {
			side, ns := "A", 90
			if strings.HasSuffix(bin, "b.test") {
				side, ns = "B", 100
			}
			log = append(log, "run:"+side+":"+execDir)
			return []byte(fmt.Sprintf("BenchmarkWork-8 1000 %d ns/op\n", ns)), nil
		},
	}
	var out, errOut bytes.Buffer
	if err := runAB(&out, &errOut, ac, []string{"."}); err != nil {
		t.Fatalf("runAB: %v\nstderr: %s", err, errOut.String())
	}
	var builds, runs []string
	for _, l := range log {
		if l == "build" {
			if len(runs) != 0 {
				t.Fatalf("a build followed a run - both sides must build first: %v", log)
			}
			builds = append(builds, l)
		} else {
			runs = append(runs, l)
		}
	}
	if len(builds) != 2 {
		t.Fatalf("builds = %v, want both sides", builds)
	}
	if len(runs) != 6 {
		t.Fatalf("runs = %v, want 3 interleaved pairs", runs)
	}
	for i, r := range runs {
		wantSide := "A"
		if i%2 == 1 {
			wantSide = "B"
		}
		if !strings.HasPrefix(r, "run:"+wantSide+":") {
			t.Fatalf("iteration %d ran %s, want strict A/B alternation: %v", i, r, runs)
		}
	}
	// Each side runs from its own tree: B lives in the disposable
	// worktree, and that worktree is gone after the run.
	aDir := strings.TrimPrefix(runs[0], "run:A:")
	bDir := strings.TrimPrefix(runs[1], "run:B:")
	if aDir == bDir {
		t.Fatalf("both sides ran from one tree: %s", aDir)
	}
	if !strings.Contains(out.String(), "A=working-tree") || !strings.Contains(out.String(), "B=HEAD") {
		t.Fatalf("report header missing the side identities:\n%s", out.String())
	}
	if _, err := os.Stat(bDir); !os.IsNotExist(err) {
		t.Fatalf("worktree survived cleanup: %v", err)
	}
}

// The artifact is marked out of every stat-baseline path by shape.
func TestABArtifactIsDirtyMarked(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ab.txt")
	if err := writeABArtifact(path, "example.com/p", "HEAD", []byte("BenchmarkA-8 1 9 ns/op\n"), []byte("BenchmarkA-8 1 10 ns/op\n")); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"pew-ab: 1", "dirty: true", "pew-ab-side: A", "pew-ab-side: B"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("artifact missing %q:\n%s", want, data)
		}
	}
}

// Recording into a new GOMAXPROCS variant lineage warns at record time:
// result names embed the suffix, comparisons never bridge it, and the
// operator must not learn that from a later stat (spec §10.1).
func TestWarnNewVariantLineage(t *testing.T) {
	dir := t.TempDir()
	st := store.New(dir)
	prior := parseBench(t, "pew-format: 2\nBenchmarkWork-24 1000 100 ns/op\n")
	if err := st.Write("p", "BenchmarkWork", "", prior); err != nil {
		t.Fatal(err)
	}
	incoming := parseBench(t, "BenchmarkWork-4 1000 90 ns/op\n")
	var errw bytes.Buffer
	warnNewVariantLineage(&errw, st, "p", "BenchmarkWork", "", incoming)
	if !strings.Contains(errw.String(), "new variant lineage (-4)") || !strings.Contains(errw.String(), "-24") {
		t.Fatalf("lineage warning missing: %q", errw.String())
	}
	// The unsuffixed lineage (GOMAXPROCS=1 emits no suffix) is a
	// lineage exactly as bridgeless as any other - the most likely
	// field shape of a narrow --pin against a wide record.
	unsuffixed := parseBench(t, "BenchmarkWork 1000 90 ns/op\n")
	errw.Reset()
	warnNewVariantLineage(&errw, st, "p", "BenchmarkWork", "", unsuffixed)
	if !strings.Contains(errw.String(), "new variant lineage (unsuffixed)") || !strings.Contains(errw.String(), "-24") {
		t.Fatalf("unsuffixed lineage warning missing: %q", errw.String())
	}
	// And the reverse: a suffixed run against an unsuffixed record.
	if err := st.Write("p", "BenchmarkBare", "", unsuffixed); err != nil {
		t.Fatal(err)
	}
	errw.Reset()
	warnNewVariantLineage(&errw, st, "p", "BenchmarkBare", "", incoming)
	if !strings.Contains(errw.String(), "new variant lineage (-4)") || !strings.Contains(errw.String(), "unsuffixed") {
		t.Fatalf("suffixed-vs-unsuffixed warning missing: %q", errw.String())
	}
	// A sub-benchmark case name ending in -<nondigits> is a name, not a
	// lineage: a GOMAXPROCS=1 run of it is the unsuffixed lineage, and
	// the warning must say so rather than misread the case name.
	subCase := parseBench(t, "BenchmarkWork/mode-fast 1000 90 ns/op\n")
	errw.Reset()
	warnNewVariantLineage(&errw, st, "p", "BenchmarkWork", "", subCase)
	if !strings.Contains(errw.String(), "(unsuffixed)") || strings.Contains(errw.String(), "-fast") {
		t.Fatalf("case-name suffix misread as lineage: %q", errw.String())
	}
	// The stored lineage warns nothing.
	same := parseBench(t, "BenchmarkWork-24 1000 95 ns/op\n")
	errw.Reset()
	warnNewVariantLineage(&errw, st, "p", "BenchmarkWork", "", same)
	if errw.Len() != 0 {
		t.Fatalf("stored lineage warned: %q", errw.String())
	}
	// A first recording has nothing to diverge from.
	errw.Reset()
	warnNewVariantLineage(&errw, st, "p", "BenchmarkNew", "", incoming)
	if errw.Len() != 0 {
		t.Fatalf("first recording warned: %q", errw.String())
	}
}

func parseBench(t *testing.T, src string) []*benchfmt.Result {
	t.Helper()
	r := benchfmt.NewReader(strings.NewReader(src), "test")
	var out []*benchfmt.Result
	for r.Scan() {
		if rec, ok := r.Result().(*benchfmt.Result); ok {
			out = append(out, rec.Clone())
		}
	}
	if err := r.Err(); err != nil || len(out) == 0 {
		t.Fatalf("parse: %v (%d rows)", err, len(out))
	}
	return out
}
