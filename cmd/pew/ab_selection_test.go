package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	git "github.com/go-git/go-git/v5"
	"github.com/greatliontech/pew/internal/run"
	"golang.org/x/perf/benchfmt"
)

func TestABRejectsRowsOutsideItsRequestedInventoryBeforePublication(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("executes retained A/B binaries and interrupts an invalid pair")
	}
	for _, bad := range []string{"BenchmarkOther-2", "BenchmarkGhost-2", "BenchmarkWork/unselected-2"} {
		t.Run(bad, func(t *testing.T) {
			root := abFixtureRepo(t)
			writeFile(t, filepath.Join(root, "p", "p_test.go"), `package p
import "testing"
func BenchmarkWork(b *testing.B) { b.Run("selected", func(b *testing.B) { for b.Loop() {} }) }
func BenchmarkOther(b *testing.B) {}
`)
			repo, err := git.PlainOpen(root)
			if err != nil {
				t.Fatal(err)
			}
			commitAll(t, repo, "requested benchmark inventory")
			ctx := withWorkingDir(t, root)
			for _, retained := range []bool{false, true} {
				ac := abConfig{bench: "^BenchmarkWork$/^selected$|^BenchmarkGhost$", count: 2, benchtime: "1x", ref: "HEAD"}
				if retained {
					ac.out = filepath.Join(t.TempDir(), "ab.txt")
					writeFile(t, ac.out, "prior artifact\n")
				}
				calls := 0
				ac.execute = func(dir, pin string, values []string, binary string, args []string) ([]byte, error) {
					calls++
					data, err := run.ExecuteBinaryContext(ctx, dir, pin, testEnvironment(t, values), binary, args)
					if err == nil && calls%2 == 0 {
						data = append(data, []byte(fmt.Sprintf("%s 1 5 ns/op\n", bad))...)
					}
					return data, err
				}
				var out bytes.Buffer
				err = runAB(ctx, &out, io.Discard, ac, []string{"./p"})
				if err == nil || !strings.Contains(err.Error(), "outside requested") || calls != 2 {
					t.Fatalf("retained=%t: foreign row became requested work: calls=%d error=%v", retained, calls, err)
				}
				if retained {
					data, err := os.ReadFile(ac.out)
					if err != nil || string(data) != "prior artifact\n" {
						t.Fatalf("invalid pair changed the committed prefix: %v\n%s", err, data)
					}
				}
			}
		})
	}
}

func FuzzABStreamAdmissionUsesTheDeclaredSelection(f *testing.F) {
	for name := byte(0); name < 3; name++ {
		for child := byte(0); child < 3; child++ {
			for count := byte(0); count < 3; count++ {
				f.Add(name, child, count, byte(7))
			}
		}
	}
	for shape := byte(1); shape < 6; shape++ {
		f.Add(shape*3, byte(0), byte(1), byte(9))
		f.Add(shape*3, byte(2), byte(1), byte(9))
	}
	f.Fuzz(func(t *testing.T, name, child, count, value byte) {
		base := []string{"BenchmarkWork", "BenchmarkGhost", "BenchmarkOther"}[name%3]
		base += []string{"", "-forged", "-1", "-02", "-2", "-+2"}[(name/3)%6]
		leaf := []string{"left", "right"}[child%2]
		pattern := "Benchmark/^left$"
		row := fmt.Sprintf("%s/%s-2 1 %d ns/op\n", base, leaf, int(value)+1)
		validName := name%3 == 0 && (name/3)%6 == 0 && child%2 == 0
		if child%3 == 2 {
			pattern = "."
			row = fmt.Sprintf("%s 1 %d ns/op\n", base, int(value)+1)
			validName = name%3 == 0 && ((name/3)%6 == 0 || (name/3)%6 == 4)
		}
		raw := []byte(strings.Repeat(row, int(count%3)))
		_, err := admitABStream(io.Discard, raw, "example.com/p", pattern, []string{"BenchmarkWork"})
		want := validName && count%3 == 1
		if (err == nil) != want {
			t.Fatalf("admitted=%t want=%t, row=%q count=%d error=%v", err == nil, want, row, count%3, err)
		}
	})
}

func TestABStreamReportsCorruptLineAndKeepsItsOwnSideHistory(t *testing.T) {
	t.Parallel()
	var warning bytes.Buffer
	if _, err := admitABStream(&warning, []byte("header\nBenchmarkWork-2 broken\n"), "example.com/p", ".", []string{"BenchmarkWork"}); err == nil || !strings.Contains(warning.String(), "line 2:") || !strings.Contains(warning.String(), `"BenchmarkWork-2 broken"`) {
		t.Fatalf("corrupt line lost its position: %v, %s", err, &warning)
	}
	for _, retained := range []bool{false, true} {
		for _, changed := range []int{0, 1, 2, 3} {
			a := &abArtifact{}
			if retained {
				a.path = filepath.Join(t.TempDir(), "ab.txt")
			}
			left := fixtureABStream(t, []byte("BenchmarkWork/left-2 1 1 ns/op\n"), "example.com/p", "BenchmarkWork")
			right := fixtureABStream(t, []byte("BenchmarkWork/right-2 1 2 ns/op\n"), "example.com/p", "BenchmarkWork")
			if err := a.pair("example.com/p", "HEAD", 1, left, right); err != nil {
				t.Fatalf("asymmetric initial pair: %v", err)
			}
			var before []byte
			if retained {
				var err error
				before, err = os.ReadFile(a.path)
				if err != nil {
					t.Fatal(err)
				}
			}
			movedRaw := []byte("BenchmarkWork/moved-2 1 2 ns/op\n")
			if changed >= 2 {
				prior := left.raw
				if changed%2 == 1 {
					prior = right.raw
				}
				movedRaw = append(append([]byte{}, prior...), movedRaw...)
			}
			moved := fixtureABStream(t, movedRaw, "example.com/p", "BenchmarkWork")
			if changed%2 == 0 {
				left = moved
			} else {
				right = moved
			}
			if err := a.pair("example.com/p", "HEAD", 2, left, right); err == nil || !strings.Contains(err.Error(), "child sample set changed") {
				t.Fatalf("retained=%t changedSide=%d accepted changed population: %v", retained, changed, err)
			}
			if retained {
				after, err := os.ReadFile(a.path)
				if err != nil || !bytes.Equal(before, after) {
					t.Fatalf("changed population replaced committed pair: %v", err)
				}
			}
		}
	}
}

func TestABUsesEachSidesOwnPackageIdentity(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("builds and executes ref-local modules with different names")
	}
	root := abFixtureRepo(t)
	writeFile(t, filepath.Join(root, "p", "p_test.go"), "package p\nimport \"testing\"\nvar sink []byte\n//gofresh:pure\nfunc BenchmarkWork(b *testing.B) { for b.Loop() { sink = make([]byte, 2<<20) } }\n")
	repo, err := git.PlainOpen(root)
	if err != nil {
		t.Fatal(err)
	}
	commitAll(t, repo, "profile-capable reference")
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/renamed\n\ngo 1.26.4\n")
	ctx := withWorkingDir(t, root)
	for _, retained := range []bool{false, true} {
		ac := abConfig{bench: "^BenchmarkWork$", count: 1, benchtime: "1x", ref: "HEAD"}
		if retained {
			ac.out = filepath.Join(t.TempDir(), "ab.txt")
		}
		var out bytes.Buffer
		if err := runAB(ctx, &out, io.Discard, ac, []string{"./p"}); err != nil {
			t.Fatalf("ref-local package identity refused: %v", err)
		}
		if !strings.Contains(out.String(), "example.com/renamed/p") || !strings.Contains(out.String(), "example.com/abfix/p") {
			t.Fatalf("comparison lost ref-local identities: %s", &out)
		}
		if retained {
			data, err := os.ReadFile(ac.out)
			if err != nil || !bytes.Contains(data, []byte("pkg: example.com/renamed/p")) || !bytes.Contains(data, []byte("pkg: example.com/abfix/p")) {
				t.Fatalf("artifact lost ref-local identities: %v\n%s", err, data)
			}
		}
	}
	var out bytes.Buffer
	err = runAB(ctx, &out, io.Discard, abConfig{bench: "^BenchmarkWork$", count: 1, benchtime: "1x", ref: "HEAD", profile: "alloc", profileBenchtime: "16x", jsonOut: true}, []string{"./p"})
	if err == nil || exitCode(err) != 2 {
		t.Fatalf("distinct subjects' diagnostic comparison = %v\n%s", err, &out)
	}
	reports, _ := decodeProfileReport(t, out.Bytes())
	if len(reports) != 1 || reports[0].Base.Integrity != "verified" || reports[0].New.Integrity != "verified" || reports[0].Base.Error != "" || reports[0].New.Error != "" || reports[0].BasePackage != "example.com/abfix/p" || reports[0].NewPackage != "example.com/renamed/p" || reports[0].Package != "" {
		t.Fatalf("diagnostic borrowed another side's subject/build identity: %+v", reports)
	}
}

func TestABArtifactHeadersKeepPreparedIdentityWithoutStreamHeaders(t *testing.T) {
	t.Parallel()
	a := &abArtifact{path: filepath.Join(t.TempDir(), "ab.txt")}
	left := fixtureABStream(t, []byte("BenchmarkWork-2 1 1 ns/op\n"), "example.com/new", "BenchmarkWork")
	right := fixtureABStream(t, []byte("BenchmarkWork-2 1 2 ns/op\n"), "example.com/old", "BenchmarkWork")
	if err := a.pair("example.com/new", "HEAD", 1, left, right); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(a.path)
	if err != nil {
		t.Fatal(err)
	}
	r := benchfmt.NewReader(bytes.NewReader(data), "artifact")
	rows := 0
	for r.Scan() {
		if row, ok := r.Result().(*benchfmt.Result); ok {
			want := map[string]string{"A": "example.com/new", "B": "example.com/old"}[row.GetConfig("pew-ab-side")]
			if want == "" || row.GetConfig("pkg") != want {
				t.Fatalf("side header borrowed package identity: %+v", row.Config)
			}
			rows++
		}
	}
	if err := r.Err(); err != nil || rows != 2 {
		t.Fatalf("artifact rows=%d error=%v", rows, err)
	}
}

func TestABNameInterpretationsCannotBorrowEachOthersScope(t *testing.T) {
	t.Parallel()
	_, err := admitABStream(io.Discard, []byte("BenchmarkWork-2 1 5 ns/op\n"), "example.com/p", "^BenchmarkWork$/^child$|^BenchmarkWork-2$", []string{"BenchmarkWork"})
	if err == nil {
		t.Fatal("one spelling supplied the declaration while another supplied the selected pattern")
	}
	if _, err := admitABStream(io.Discard, []byte("BenchmarkWork/child-2 1 5 ns/op\n"), "example.com/p", "^BenchmarkWork$/^child$|^BenchmarkWork-2$", []string{"BenchmarkWork"}); err != nil {
		t.Fatalf("valid child failed combined admission: %v", err)
	}
}

func TestABUsesBuildSelectedReferenceDeclarationsBeforeBuilding(t *testing.T) {
	if testing.Short() {
		t.Skip("lists two ref-local package inventories")
	}
	root := abFixtureRepo(t)
	writeFile(t, filepath.Join(root, "p", "p_test.go"), "package p\nimport \"testing\"\nfunc BenchmarkOther(b *testing.B) {}\n")
	writeFile(t, filepath.Join(root, "p", "hidden_test.go"), "//go:build unselected\n\npackage p\nimport \"testing\"\nfunc BenchmarkWork(b *testing.B) {}\n")
	repo, err := git.PlainOpen(root)
	if err != nil {
		t.Fatal(err)
	}
	commitAll(t, repo, "excluded reference declaration")
	writeFile(t, filepath.Join(root, "p", "hidden_test.go"), "package p\nimport \"testing\"\nfunc BenchmarkWork(b *testing.B) {}\n")
	ctx := withWorkingDir(t, root)
	t.Setenv("GOFLAGS", "")
	builds := 0
	ac := abConfig{bench: "^BenchmarkWork$", count: 1, benchtime: "1x", ref: "HEAD", build: func(string, []string, []string) error {
		builds++
		return fmt.Errorf("unexpected build before declaration refusal")
	}}
	err = runAB(ctx, io.Discard, io.Discard, ac, []string{"./p"})
	if err == nil || !strings.Contains(err.Error(), "selects no benchmark on side B") || builds != 0 {
		t.Fatalf("compiled-out reference declaration was requested: builds=%d error=%v", builds, err)
	}
}

func TestABPreservesTheValidPairBeforeARejectedSide(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("executes a valid pair before rejecting a later side")
	}
	root := abFixtureRepo(t)
	ctx := withWorkingDir(t, root)
	ac := abConfig{bench: "^BenchmarkWork$", count: 2, benchtime: "1x", ref: "HEAD", out: filepath.Join(t.TempDir(), "ab.txt")}
	calls := 0
	ac.execute = func(dir, pin string, values []string, binary string, args []string) ([]byte, error) {
		calls++
		data, err := run.ExecuteBinaryContext(ctx, dir, pin, testEnvironment(t, values), binary, args)
		if err == nil && calls == 3 {
			data = append(data, []byte("BenchmarkOther-2 1 5 ns/op\n")...)
		}
		return data, err
	}
	err := runAB(ctx, io.Discard, io.Discard, ac, []string{"./p"})
	if err == nil || !strings.Contains(err.Error(), "side A iteration 2") || !strings.Contains(err.Error(), "outside requested") || calls != 3 {
		t.Fatalf("invalid side paid another execution or lost its attribution: calls=%d error=%v", calls, err)
	}
	data, err := os.ReadFile(ac.out)
	if err != nil || strings.Count(string(data), "pew-ab-section: measurement") != 2 || bytes.Contains(data, []byte("BenchmarkOther")) {
		t.Fatalf("refused side damaged the completed prefix: %v\n%s", err, data)
	}
}

func TestABAdmitsEachSidesDistinctSelectedPopulation(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("compares independently selected reference populations")
	}
	root := abFixtureRepo(t)
	path := filepath.Join(root, "p", "p_test.go")
	writeFile(t, path, "package p\nimport \"testing\"\nfunc BenchmarkBefore(b *testing.B) { b.Run(\"old\", func(b *testing.B) { for b.Loop() {} }) }\n")
	repo, err := git.PlainOpen(root)
	if err != nil {
		t.Fatal(err)
	}
	commitAll(t, repo, "reference-only benchmark")
	writeFile(t, path, "package p\nimport \"testing\"\nfunc BenchmarkAfter(b *testing.B) { b.Run(\"new\", func(b *testing.B) { for b.Loop() {} }) }\n")
	ctx := withWorkingDir(t, root)
	ac := abConfig{bench: "^Benchmark(Before|After)$", count: 1, benchtime: "1x", ref: "HEAD", out: filepath.Join(t.TempDir(), "ab.txt")}
	if err := runAB(ctx, io.Discard, io.Discard, ac, []string{"./p"}); err != nil {
		t.Fatalf("valid asymmetric population refused: %v", err)
	}
	data, err := os.ReadFile(ac.out)
	if err != nil || !bytes.Contains(data, []byte("BenchmarkBefore/old")) || !bytes.Contains(data, []byte("BenchmarkAfter/new")) {
		t.Fatalf("side population was lost: %v\n%s", err, data)
	}
}
