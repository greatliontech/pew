package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	git "github.com/go-git/go-git/v5"
	"github.com/greatliontech/pew/internal/gotool"
	"github.com/greatliontech/pew/internal/recordingtest"
	"github.com/greatliontech/pew/internal/run"
	"github.com/greatliontech/pew/internal/store"
)

func TestABOutputOwnershipRefusesBeforeBuildOrMeasurement(t *testing.T) {
	root := abFixtureRepo(t)
	st := store.New(filepath.Join(root, "benchmarks"))
	if err := st.Write("p", "BenchmarkWork", "", recordingtest.Results("Work", []float64{1, 2})); err != nil {
		t.Fatal(err)
	}
	record, _ := st.Path("p", "BenchmarkWork", "")
	external := filepath.Join(t.TempDir(), "result.txt")
	writeFile(t, external, "keep external")
	fileLink := filepath.Join(root, "linked.txt")
	if err := os.Symlink(external, fileLink); err != nil {
		t.Fatal(err)
	}
	dirLink := filepath.Join(root, "linked-dir")
	if err := os.Symlink(filepath.Dir(external), dirLink); err != nil {
		t.Fatal(err)
	}
	profileOut := filepath.Join(root, "profile-output.txt")
	if err := os.Symlink(st.Root, profileOut+".profiles"); err != nil {
		t.Fatal(err)
	}
	withWorkingDir(t, root)
	for _, tc := range []struct{ name, path, profile string }{
		{"recording", record, ""}, {"lock", filepath.Join(st.Root, ".pew-lock"), ""}, {"source", filepath.Join(root, "p", "p.go"), ""}, {"module", filepath.Join(root, "go.mod"), ""},
		{"symlink-file", fileLink, ""}, {"symlink-directory", filepath.Join(dirLink, "result.txt"), ""}, {"companion-store", profileOut, "cpu"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			prior, readErr := os.ReadFile(tc.path)
			ac := abConfigStub("")
			ac.out = tc.path
			ac.profile = tc.profile
			builds, runs := 0, 0
			ac.build = func(string, []string, []string) error { builds++; return nil }
			ac.execute = func(string, string, []string, string, []string) ([]byte, error) {
				runs++
				return []byte("BenchmarkWork-8 1 1 ns/op\n"), nil
			}
			err := runAB(t.Context(), io.Discard, io.Discard, ac, []string{"./p"})
			if err == nil || !strings.Contains(err.Error(), "output") || builds != 0 || runs != 0 {
				t.Fatalf("preflight: err=%v builds=%d runs=%d", err, builds, runs)
			}
			if readErr == nil {
				after, err := os.ReadFile(tc.path)
				if err != nil || !bytes.Equal(prior, after) {
					t.Fatalf("protected bytes changed: %v", err)
				}
			}
		})
	}
}

func TestABOutputOwnsOnlySelectedSourcesAndStores(t *testing.T) {
	root := abFixtureRepo(t)
	dependency := filepath.Join(filepath.Dir(root), "dep")
	writeFile(t, filepath.Join(dependency, "go.mod"), "module example.com/dep\ngo 1.24\n")
	input := filepath.Join(dependency, "dep.go")
	writeFile(t, input, "package dep\nfunc Value() int {return 1}\n")
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/abfix\ngo 1.24\nrequire example.com/dep v0.0.0\nreplace example.com/dep => ../dep\n")
	writeFile(t, filepath.Join(root, "p", "p.go"), "package p\nimport \"example.com/dep\"\nfunc Work(n int) int {return dep.Value()+n}\n")
	withWorkingDir(t, root)
	env := testEnvironment(t, nil)
	pkgs, err := resolvePackages(t.Context(), env, []string{"./p"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := prepareABOutput(t.Context(), input, false, pkgs, env); err == nil || !strings.Contains(err.Error(), "source input") {
		t.Fatalf("selected external dependency not protected: %v", err)
	}
	for _, name := range []string{"unrelated.go", ".pew-lock"} {
		out := filepath.Join(t.TempDir(), name)
		writeFile(t, out, "unrelated output")
		if _, err := prepareABOutput(t.Context(), out, false, pkgs, env); err != nil {
			t.Fatalf("guessed ownership from %s: %v", name, err)
		}
	}
}

func TestABOutputRechecksTopologyBeforePublication(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.txt")
	owned := &abOutputOwnership{path: out}
	if err := owned.validate(); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "keep.txt")
	writeFile(t, target, "keep")
	if err := os.Symlink(target, out); err != nil {
		t.Fatal(err)
	}
	a := &abArtifact{path: out, ownership: owned}
	if err := a.pair("example.com/p", "HEAD", 1, []byte("BenchmarkWork-8 1 1 ns/op\n"), []byte("BenchmarkWork-8 1 2 ns/op\n")); err == nil {
		t.Fatal("changed topology accepted")
	}
	info, err := os.Lstat(out)
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("replaced changed destination: %v", err)
	}
	b, err := os.ReadFile(target)
	if err != nil || string(b) != "keep" {
		t.Fatal(fmt.Sprintf("target changed: %s %v", b, err))
	}
}

func TestABOutputProtectsSelectedBuildConfiguration(t *testing.T) {
	root := abFixtureRepo(t)
	workspace := filepath.Join(root, "go.work")
	writeFile(t, workspace, "go 1.24\nuse .\n")
	pgo := filepath.Join(root, "selected.pprof")
	writeFile(t, pgo, "selected build input")
	t.Setenv("GOWORK", workspace)
	t.Setenv("GOFLAGS", "-pgo="+pgo)
	withWorkingDir(t, root)
	var p pkgMeta
	p.ImportPath = "example.com/abfix/p"
	p.Dir = filepath.Join(root, "p")
	p.Name = "p"
	p.Module.Dir = root
	p.Module.Path = "example.com/abfix"
	for _, path := range []string{workspace, pgo} {
		if _, err := prepareABOutput(t.Context(), path, false, []pkgMeta{p}, testEnvironment(t, nil)); err == nil || !strings.Contains(err.Error(), "source input") {
			t.Fatalf("build input %s not protected: %v", path, err)
		}
	}
}

func TestABOutputProtectsReferenceOnlyExternalInputWithoutProfiles(t *testing.T) {
	if testing.Short() {
		t.Skip("warms a real external dependency build and inventories the materialized ref")
	}
	root := abFixtureRepo(t)
	dependency := t.TempDir()
	writeFile(t, filepath.Join(dependency, "go.mod"), "module example.com/external\ngo 1.24\n")
	input := filepath.Join(dependency, "selected.go")
	content := []byte("package external\nfunc Value() int {return 7}\n")
	writeFile(t, input, string(content))
	writeFile(t, filepath.Join(root, "go.mod"), fmt.Sprintf("module example.com/abfix\ngo 1.24\nrequire example.com/external v0.0.0\nreplace example.com/external => %q\n", filepath.ToSlash(dependency)))
	writeFile(t, filepath.Join(root, "p", "p.go"), "package p\nimport \"example.com/external\"\nfunc Work(n int) int {return external.Value()+n}\n")
	repo, err := git.PlainOpen(root)
	if err != nil {
		t.Fatal(err)
	}
	commitAll(t, repo, "reference imports external input")
	withWorkingDir(t, root)
	env := testEnvironment(t, nil)
	// A warm compiled dependency must still contribute its selected source paths.
	if err := run.BuildContext(t.Context(), root, env, []string{"test", "-c", "-o", filepath.Join(t.TempDir(), "warm.test"), "./p"}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "p", "p.go"), "package p\nfunc Work(n int) int {return n}\n")
	pkgs, err := resolvePackages(t.Context(), env, []string{"./p"})
	if err != nil {
		t.Fatal(err)
	}
	// The target really is absent from A's selected input set. No filename-based
	// exemption or protection can substitute for inventorying B's own tree.
	if _, err := prepareABOutput(t.Context(), input, false, pkgs, env); err != nil {
		t.Fatalf("fixture target is not B-only: %v", err)
	}
	ac := abConfigStub("")
	ac.out = input
	builds, measured := 0, 0
	ac.build = func(string, []string, []string) error { builds++; return nil }
	ac.execute = func(string, string, []string, string, []string) ([]byte, error) {
		measured++
		return []byte("BenchmarkWork-8 1 1 ns/op\n"), nil
	}
	err = runAB(t.Context(), io.Discard, io.Discard, ac, []string{"./p"})
	if err == nil || !strings.Contains(err.Error(), "source input") || !strings.Contains(err.Error(), input) || builds != 0 || measured != 0 {
		t.Errorf("B-only output preflight: err=%v builds=%d measurements=%d", err, builds, measured)
	}
	after, err := os.ReadFile(input)
	if err != nil || !bytes.Equal(after, content) {
		t.Errorf("B's source overwritten: %q %v", after, err)
	}
}

func TestABOutputInventoryRetainsStandardSourcePaths(t *testing.T) {
	root := abFixtureRepo(t)
	withWorkingDir(t, root)
	env := testEnvironment(t, nil)
	pkgs, err := resolvePackages(t.Context(), env, []string{"./p"})
	if err != nil {
		t.Fatal(err)
	}
	goroot, err := gotool.EnvValue(t.Context(), gotool.Reader(root, env, nil), "GOROOT")
	if err != nil {
		t.Fatal(err)
	}
	input, err := filepath.EvalSymlinks(filepath.Join(goroot, "src", "testing", "testing.go"))
	if err != nil {
		t.Fatal(err)
	}
	// Admission only: this test never attempts to write a toolchain source file.
	if _, err := prepareABOutput(t.Context(), input, false, pkgs, env); err == nil || !strings.Contains(err.Error(), "source input") {
		t.Fatalf("selected standard source omitted: %v", err)
	}
}

func TestABOutputUsesSelectedTestVariantInputs(t *testing.T) {
	root := abFixtureRepo(t)
	dependency := t.TempDir()
	writeFile(t, filepath.Join(dependency, "go.mod"), "module example.com/dep\ngo 1.24\n")
	writeFile(t, filepath.Join(dependency, "dep.go"), `package dep
import _ "embed"
//go:embed production.txt
var production string
func Value() int { return len(production) }
`)
	writeFile(t, filepath.Join(dependency, "unrelated_test.go"), `package dep
import (_ "embed"; "testing")
//go:embed testdata/internal.txt
var internalData string
func TestInternal(t *testing.T) { if internalData=="" {t.Fatal("missing embedded test input")} }
func BenchmarkDep(b *testing.B) { for i:=0;i<b.N;i++ { _ = Value() } }
`)
	writeFile(t, filepath.Join(dependency, "external_test.go"), `package dep_test
import (_ "embed"; "testing"; "example.com/dep")
//go:embed testdata/external.txt
var externalData string
func TestExternal(t *testing.T) { if externalData=="" || dep.Value()==0 {t.Fatal("missing input")} }
`)
	for _, file := range []string{"production.txt", "testdata/internal.txt", "testdata/external.txt"} {
		writeFile(t, filepath.Join(dependency, file), "input")
	}
	writeFile(t, filepath.Join(root, "go.mod"), fmt.Sprintf("module example.com/abfix\ngo 1.24\nrequire example.com/dep v0.0.0\nreplace example.com/dep => %q\n", filepath.ToSlash(dependency)))
	writeFile(t, filepath.Join(root, "p", "p.go"), "package p\nimport \"example.com/dep\"\nfunc Work(n int) int {return dep.Value()+n}\n")
	repo, err := git.PlainOpen(root)
	if err != nil {
		t.Fatal(err)
	}
	commitAll(t, repo, "selected dependency variants")
	withWorkingDir(t, root)
	env := testEnvironment(t, nil)
	for _, selection := range []struct {
		name, pattern   string
		dependencyTests bool
	}{
		{"caller-only", "./p", false}, {"dependency-tested", "example.com/dep", true},
	} {
		pkgs, err := resolvePackages(t.Context(), env, []string{selection.pattern})
		if err != nil {
			t.Fatal(err)
		}
		for _, file := range []struct {
			name       string
			production bool
		}{
			{"dep.go", true}, {"production.txt", true}, {"unrelated_test.go", false}, {"external_test.go", false}, {"testdata/internal.txt", false}, {"testdata/external.txt", false},
		} {
			t.Run(selection.name+"/"+file.name, func(t *testing.T) {
				_, err := prepareABOutput(t.Context(), filepath.Join(dependency, file.name), false, pkgs, env)
				if file.production || selection.dependencyTests {
					if err == nil || !strings.Contains(err.Error(), "source input") {
						t.Fatalf("selected variant input %s not protected: %v", file.name, err)
					}
				} else if err != nil {
					t.Fatalf("unselected dependency test input %s refused: %v", file.name, err)
				}
			})
		}
	}
	// Both materialized sides import dep, but neither selects dep's own tests.
	// Exercise the actual --out path after the non-writing admission checks.
	t.Run("artifact", func(t *testing.T) {
		if testing.Short() {
			t.Skip("builds and measures both native benchmark binaries")
		}
		ac := abConfig{bench: "^BenchmarkWork$", count: 1, benchtime: "1x", ref: "HEAD", out: filepath.Join(dependency, "unrelated_test.go")}
		if err := runAB(t.Context(), io.Discard, io.Discard, ac, []string{"./p"}); err != nil {
			t.Fatalf("unselected dependency test output refused end-to-end: %v", err)
		}
		artifact, err := os.ReadFile(ac.out)
		if err != nil || !bytes.Contains(artifact, []byte("pew-ab: 2\n")) {
			t.Fatalf("allowed output not published: %s %v", artifact, err)
		}
	})
}

func TestABOutputDoesNotSelectStandardDependencyTests(t *testing.T) {
	root := abFixtureRepo(t)
	withWorkingDir(t, root)
	env := testEnvironment(t, nil)
	pkgs, err := resolvePackages(t.Context(), env, []string{"./p"})
	if err != nil {
		t.Fatal(err)
	}
	data, err := gotool.List(t.Context(), root, env, "-json", "testing")
	if err != nil {
		t.Fatal(err)
	}
	var metadata struct {
		Dir                       string
		TestGoFiles, XTestGoFiles []string
	}
	if err := json.Unmarshal(data, &metadata); err != nil {
		t.Fatal(err)
	}
	files := append(metadata.TestGoFiles, metadata.XTestGoFiles...)
	if len(files) == 0 {
		t.Fatal("toolchain fixture has no testing-package test metadata")
	}
	input, err := filepath.EvalSymlinks(filepath.Join(metadata.Dir, files[0]))
	if err != nil {
		t.Fatal(err)
	}
	// Admission only; never write into the toolchain. testing's production
	// inputs are selected by our test binary, but testing's own tests are not.
	if _, err := prepareABOutput(t.Context(), input, false, pkgs, env); err != nil {
		t.Fatalf("unselected standard-library test refused: %v", err)
	}
}
