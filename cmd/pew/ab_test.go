package main

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh/guard"
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
	// The worktree is a SIBLING of the repository — same filesystem, so
	// package-dir-relative benchmark media sees the same storage on both
	// sides (an os.TempDir worktree on a tmpfs host measures RAM against
	// disk). Both paths resolve through the same symlink discipline
	// before comparison.
	aRoot, err := filepath.EvalSymlinks(filepath.Dir(filepath.Dir(aDir)))
	if err != nil {
		t.Fatal(err)
	}
	bRoot, err := filepath.EvalSymlinks(filepath.Dir(filepath.Dir(bDir)))
	if err != nil {
		t.Fatal(err)
	}
	if aRoot != bRoot {
		t.Fatalf("worktree parent %s is not beside the repository (parent %s) — side B's media lives on a different filesystem", bRoot, aRoot)
	}
	if !strings.Contains(out.String(), "A=working-tree") || !strings.Contains(out.String(), "B=HEAD") {
		t.Fatalf("report header missing the side identities:\n%s", out.String())
	}
	if _, err := os.Stat(bDir); !os.IsNotExist(err) {
		t.Fatalf("worktree survived cleanup: %v", err)
	}
}

// The comparator enforces §10.1's guard provenance; raw go-test streams
// carry none, so ab must stamp each side's captured guards or every
// benchmark refuses with "missing machine provenance" and the command
// reports no verdict at all. Shared guards must yield a real comparison
// table; a genuine per-side difference in a build-identity guard must
// still refuse with the mismatch named.
func TestABStampsGuardProvenance(t *testing.T) {
	dir := abFixtureRepo(t)
	prior, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(filepath.Join(dir, "p")); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(prior)

	base := abConfig{
		bench: ".", count: 3, ref: "HEAD",
		throttle: func() run.ThrottleSnapshot { return run.ThrottleSnapshot{} },
		build:    func(string, []string, []string) error { return nil },
	}

	shared := base
	var events []string
	var sharedDirs, buildDirs, execDirs []string
	var buildArgs [][]string
	shared.build = func(buildDir string, env []string, args []string) error {
		buildDirs = append(buildDirs, buildDir)
		buildArgs = append(buildArgs, args)
		return nil
	}
	shared.guards = func(moduleDir, pkgDir string, mainPkg bool, env []string) (guard.Guards, error) {
		events = append(events, "guards")
		sharedDirs = append(sharedDirs, moduleDir)
		return guard.Guards{Toolchain: "go1", BuildConfig: "b1", Machine: "m1", RuntimeConfig: "r1"}, nil
	}
	shared.execute = func(execDir, pin string, env []string, bin string, args []string) ([]byte, error) {
		events = append(events, "run")
		execDirs = append(execDirs, execDir)
		ns := 90
		if strings.HasSuffix(bin, "b.test") {
			ns = 100
		}
		return []byte(fmt.Sprintf("BenchmarkWork-8 1000 %d ns/op\n", ns)), nil
	}
	var out, errOut bytes.Buffer
	if err := runAB(&out, &errOut, shared, []string{"."}); err != nil {
		t.Fatalf("runAB: %v\nstderr: %s", err, errOut.String())
	}
	if strings.Contains(out.String(), "not compared") {
		t.Fatalf("guard-satisfied sides refused:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "sec/op") || !strings.Contains(out.String(), "vs base") {
		t.Fatalf("no comparison table for guard-satisfied sides:\n%s", out.String())
	}
	// Each side's guards are captured in its OWN tree (side B's build
	// identity lives at the ref's worktree), and captured at BUILD time:
	// the repository stays writable during measurement, so a stamp taken
	// afterwards could describe an edit the standing binaries never saw.
	if len(sharedDirs) != 2 || sharedDirs[0] == sharedDirs[1] {
		t.Fatalf("guard capture dirs = %v, want one per side in distinct trees", sharedDirs)
	}
	// The invariant is that the STAMPED captures precede measurement — a
	// future post-measurement revalidation capture would be legal, so the
	// assertion demands both sides captured before the first run rather
	// than forbidding late guard events outright.
	if len(events) < 2 || events[0] != "guards" || events[1] != "guards" {
		t.Fatalf("both sides' guard captures must precede measurement — stamps carry build-time identity: %v", events)
	}
	// Builds run at each side's MODULE root (the parent of the package
	// dir the side executes in): a relative -pgo in GOFLAGS resolves
	// against the build cwd, and the guard digest pins the module-root
	// resolution.
	if len(buildDirs) != 2 || len(execDirs) < 2 {
		t.Fatalf("buildDirs = %v, execDirs = %v", buildDirs, execDirs)
	}
	for i := range 2 {
		if filepath.Dir(execDirs[i]) != buildDirs[i] {
			t.Fatalf("side %d built at %s but ran at %s — build cwd must be the module root the guard digested", i, buildDirs[i], execDirs[i])
		}
	}
	for i, args := range buildArgs {
		if got := args[len(args)-1]; got != "./p" {
			t.Fatalf("side %d build target = %q, want the package relative to the module root", i, got)
		}
	}

	// A per-side build-identity difference is a genuine mismatch: the
	// refusal machinery must stay live through the stamping, and side B
	// (the ref) must land as base. The working-tree module dir repeats
	// across runs while each run's worktree is unique, so membership in
	// the first run's dirs identifies side A without any call-order
	// assumption.
	split := base
	split.execute = shared.execute
	split.guards = func(moduleDir, pkgDir string, mainPkg bool, env []string) (guard.Guards, error) {
		g := guard.Guards{Toolchain: "go1", BuildConfig: "b1", Machine: "m1", RuntimeConfig: "r1"}
		seenInSharedRun := false
		for _, d := range sharedDirs {
			if d == moduleDir {
				seenInSharedRun = true
			}
		}
		if !seenInSharedRun {
			g.Toolchain = "go2"
		}
		return g, nil
	}
	out.Reset()
	errOut.Reset()
	if err := runAB(&out, &errOut, split, []string{"."}); err != nil {
		t.Fatalf("runAB: %v\nstderr: %s", err, errOut.String())
	}
	if !strings.Contains(out.String(), "toolchain mismatch (base=go2 new=go1)") || !strings.Contains(out.String(), "not compared") {
		t.Fatalf("differing toolchain guards did not refuse with side B as base:\n%s", out.String())
	}
}

// Side B's package kind is the REF's, not the working tree's: default.pgo
// applies to a tested main package, so a package that is main at the ref
// but a library in the working tree must still resolve side B's PGO as
// main — deriving both sides' kind from the working tree silently passes
// a genuine per-ref PGO difference through the buildconfig guard.
func TestABSideBPackageKindFromRef(t *testing.T) {
	dir := abFixtureRepo(t)
	// The commit holds package main; the working tree renames it to a
	// library. go list resolves the working tree, the worktree holds the
	// ref's shape.
	mainSrc := "package main\n\nfunc main() {}\n\nfunc Work(n int) int {\n\ts := 0\n\tfor i := 0; i < n; i++ {\n\t\ts += i\n\t}\n\treturn s\n}\n"
	mainTest := "package main\n\nimport \"testing\"\n\nfunc BenchmarkWork(b *testing.B) {\n\tfor i := 0; i < b.N; i++ {\n\t\tWork(100)\n\t}\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "p/p.go"), []byte(mainSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "p/p_test.go"), []byte(mainTest), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"add", "-A"},
		{"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "main-shape"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	librarySrc := "package p\n\nfunc Work(n int) int {\n\ts := 0\n\tfor i := 0; i < n; i++ {\n\t\ts += i\n\t}\n\treturn s\n}\n"
	libraryTest := "package p\n\nimport \"testing\"\n\nfunc BenchmarkWork(b *testing.B) {\n\tfor i := 0; i < b.N; i++ {\n\t\tWork(100)\n\t}\n}\n"
	if err := os.WriteFile(filepath.Join(dir, "p/p.go"), []byte(librarySrc), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "p/p_test.go"), []byte(libraryTest), 0o644); err != nil {
		t.Fatal(err)
	}
	prior, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(filepath.Join(dir, "p")); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(prior)

	kinds := map[string]bool{}
	ac := abConfig{
		bench: ".", count: 1, ref: "HEAD",
		throttle: func() run.ThrottleSnapshot { return run.ThrottleSnapshot{} },
		build:    func(string, []string, []string) error { return nil },
		execute: func(execDir, pin string, env []string, bin string, args []string) ([]byte, error) {
			return []byte("BenchmarkWork-8 1000 100 ns/op\n"), nil
		},
		guards: func(moduleDir, pkgDir string, mainPkg bool, env []string) (guard.Guards, error) {
			kinds[moduleDir] = mainPkg
			return guard.Guards{Toolchain: "go1", BuildConfig: "b1", Machine: "m1", RuntimeConfig: "r1"}, nil
		},
	}
	var out, errOut bytes.Buffer
	if err := runAB(&out, &errOut, ac, []string{"."}); err != nil {
		t.Fatalf("runAB: %v\nstderr: %s", err, errOut.String())
	}
	// Side A is the fixture module (the working tree, a library); the
	// other capture dir is the ref's worktree, main at the ref. Symlink
	// resolution matches go list's.
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(kinds) != 2 {
		t.Fatalf("guard capture dirs = %v, want one per side", kinds)
	}
	seenA, seenB := false, false
	for d, mainPkg := range kinds {
		if d == resolved {
			seenA = true
			if mainPkg {
				t.Fatalf("side A package kind = main, want the working tree's library shape")
			}
		} else {
			seenB = true
			if !mainPkg {
				t.Fatalf("side B package kind = library, want the ref's main shape")
			}
		}
	}
	if !seenA || !seenB {
		t.Fatalf("capture dirs %v did not cover both sides (fixture root %s)", kinds, resolved)
	}
}

// sameDevice is the runtime enforcement behind the same-medium worktree
// contract: same path trivially agrees, and where this host's temp dir
// and working directory sit on different devices (tmpfs vs disk — the
// exact configuration the contract exists for), the check must say so.
func TestSameDevice(t *testing.T) {
	dir := t.TempDir()
	if same, err := sameDevice(dir, dir); err != nil || !same {
		t.Fatalf("sameDevice(x, x) = %v, %v", same, err)
	}
	if _, err := sameDevice(dir, filepath.Join(dir, "absent")); err == nil {
		t.Fatal("sameDevice on a missing path must error")
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	same, err := sameDevice(dir, wd)
	if err != nil {
		t.Fatal(err)
	}
	if same {
		t.Skipf("temp dir %s and %s share a device on this host; cross-device branch not exercisable", dir, wd)
	}
	// Reaching here proves the false branch fires on genuinely distinct
	// devices — the branch addWorktree's refusal rides on.
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
