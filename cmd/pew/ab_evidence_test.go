package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	git "github.com/go-git/go-git/v5"
	"github.com/greatliontech/pew/internal/gotool"
	"github.com/greatliontech/pew/internal/profiles"
	"github.com/greatliontech/pew/internal/run"
	"github.com/greatliontech/pew/internal/store"
	"golang.org/x/perf/benchfmt"
)

func FuzzABArtifactPairs(f *testing.F) {
	f.Add([]byte{1, 2, 3, 4})
	f.Add([]byte{255, 0})
	f.Fuzz(func(t *testing.T, values []byte) {
		path := filepath.Join(t.TempDir(), "out.txt")
		a := &abArtifact{path: path}
		for i, v := range values {
			pkg := fmt.Sprintf("example.com/p%d", i%3)
			raw := func(side string) []byte {
				return []byte(fmt.Sprintf("custom: %s\npkg: %s\nBenchmarkWork-8 7 %d ns/op\nPASS\n", side, pkg, int(v)+1))
			}
			if err := a.pair(pkg, "ref", i+1, raw("A"), raw("B")); err != nil {
				t.Fatal(err)
			}
		}
		if len(values) == 0 {
			return
		}
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		r := benchfmt.NewReader(bytes.NewReader(b), path)
		n := 0
		for r.Scan() {
			switch row := r.Result().(type) {
			case *benchfmt.SyntaxError:
				t.Fatal(row)
			case *benchfmt.Result:
				pair := n / 2
				if row.GetConfig("pew-ab") != "2" || row.GetConfig("pew-ab-pair") != strconv.Itoa(pair+1) || row.GetConfig("pkg") != fmt.Sprintf("example.com/p%d", pair%3) || row.GetConfig("pew-ab-side") != []string{"A", "B"}[n%2] || row.Iters != 7 || row.Values[0].Value != float64(int(values[pair])+1)*1e-9 {
					t.Fatalf("corrupt attribution/sample: %+v", row)
				}
				n++
			}
		}
		if err := r.Err(); err != nil {
			t.Fatal(err)
		}
		if n != 2*len(values) {
			t.Fatalf("lost completed rows: %d", n)
		}
	})
}

func TestABMultiPackageInterruptedKeepsPairs(t *testing.T) {
	if testing.Short() {
		t.Skip("fixture package listing")
	}
	root := abFixtureRepo(t)
	writeFile(t, filepath.Join(root, "q", "q_test.go"), "package q\nimport \"testing\"\nfunc BenchmarkWork(b *testing.B) {}\n")
	repo, err := git.PlainOpen(root)
	if err != nil {
		t.Fatal(err)
	}
	commitAll(t, repo, "two packages")
	withWorkingDir(t, root)
	for _, interrupt := range []bool{false, true} {
		t.Run(fmt.Sprint(interrupt), func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			ac := abConfigStub("")
			ac.count = 3
			ac.out = filepath.Join(t.TempDir(), "ab.txt")
			calls := 0
			ac.execute = func(dir, pin string, env []string, bin string, args []string) ([]byte, error) {
				calls++
				if interrupt && calls == 10 {
					cancel()
					return nil, context.Canceled
				}
				return []byte(fmt.Sprintf("BenchmarkWork-8 1 %d ns/op\nPASS\n", calls)), nil
			}
			err := runAB(ctx, io.Discard, io.Discard, ac, []string{"./..."})
			if interrupt {
				if exitCode(err) != 130 {
					t.Fatalf("exit=%v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			b, e := os.ReadFile(ac.out)
			if e != nil {
				t.Fatal(e)
			}
			r := benchfmt.NewReader(bytes.NewReader(b), ac.out)
			n := 0
			packages := map[string]int{}
			for r.Scan() {
				switch row := r.Result().(type) {
				case *benchfmt.SyntaxError:
					t.Fatal(row)
				case *benchfmt.Result:
					n++
					packages[row.GetConfig("pkg")]++
					if row.Values[0].Value != float64(n)*1e-9 {
						t.Fatal("samples changed")
					}
				}
			}
			want := 12
			if interrupt {
				want = 8
			}
			if n != want || len(packages) != 2 || packages["example.com/abfix/p"] != 6 {
				t.Fatalf("retention=%v rows=%d want=%d", packages, n, want)
			}
		})
	}
}

func TestRealABProfilesHaveIndependentSourceEvidence(t *testing.T) {
	if testing.Short() {
		t.Skip("real two-side profile processes and typed source views")
	}
	root := abFixtureRepo(t)
	source := `package p
import "testing"
var sink []byte
var number uint64
//gofresh:pure
func BenchmarkWork(b *testing.B) { for i:=0;i<b.N;i++ {sink=make([]byte,128<<10);for j:=0;j<4096;j++ {number=number*1664525+uint64(j)+uint64(Work(1))+1013904223}} }
`
	writeFile(t, filepath.Join(root, "p", "p_test.go"), source)
	repo, err := git.PlainOpen(root)
	if err != nil {
		t.Fatal(err)
	}
	commitAll(t, repo, "profile workload")
	production, err := os.ReadFile(filepath.Join(root, "p", "p.go"))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "p", "p.go"), strings.Replace(string(production), "return s", "return s+1", 1))
	withWorkingDir(t, root)
	ac := abConfig{bench: "^BenchmarkWork$", count: 2, benchtime: "1x", ref: "HEAD", profile: "cpu,alloc", profileBenchtime: "300ms", jsonOut: true, out: filepath.Join(t.TempDir(), "ab.txt")}
	var stdout, stderr bytes.Buffer
	if err := runAB(t.Context(), &stdout, &stderr, ac, []string{"./p"}); err != nil {
		t.Fatalf("%v\n%s\n%s", err, &stdout, &stderr)
	}
	decoder := json.NewDecoder(&stdout)
	profilesSeen := 0
	for decoder.More() {
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			t.Fatal(err)
		}
		var report profileComparison
		if err := json.Unmarshal(raw, &report); err != nil {
			t.Fatal(err)
		}
		if report.Kind != "profile" {
			continue
		}
		profilesSeen++
		if !report.Compared || report.Base.Outcome != "identity-only" || report.New.Outcome != "identity-only" {
			t.Fatalf("%s", raw)
		}
	}
	if profilesSeen != 2 {
		t.Fatalf("profile reports=%d", profilesSeen)
	}
	if _, err := os.Stat(filepath.Join(root, "benchmarks")); !os.IsNotExist(err) {
		t.Fatalf("normal store touched: %v", err)
	}
	b, err := os.ReadFile(ac.out)
	if err != nil {
		t.Fatal(err)
	}
	// Index sections have no measurement rows. Decode the same continuation grammar.
	var captures []profiles.Capture
	for _, block := range strings.Split(string(b), "pew-ab: 2\n")[1:] {
		if !strings.Contains(block, "pew-ab-section: profile\n") {
			continue
		}
		var encoded string
		for _, line := range strings.Split(block, "\n") {
			key, value, ok := strings.Cut(line, ": ")
			if ok && (key == "pew-profiles" || strings.HasPrefix(key, "pew-profiles.")) {
				encoded += value
			}
		}
		index, err := profiles.Decode(encoded)
		if err != nil {
			t.Fatal(err)
		}
		captures = append(captures, index.Captures...)
	}
	if len(captures) != 4 {
		t.Fatalf("retained captures=%d", len(captures))
	}
	objects := store.New(ac.out + ".profiles")
	closures := map[string]bool{}
	for _, c := range captures {
		closures[c.Fingerprint.MaximalClosure] = true
		if len(c.Sources) == 0 {
			t.Fatal("source evidence absent")
		}
		raw, err := objects.ReadObject(c.SHA256, c.Size)
		if err != nil {
			t.Fatal(err)
		}
		m, err := profiles.Analyze(raw, c.Kind, c.Sources)
		if err != nil || profiles.Empty(m) {
			t.Fatalf("%v %v", m, err)
		}
	}
	if len(closures) != 2 {
		t.Fatalf("source edits lost: %v", closures)
	}
	ac.profile = "alloc"
	ac.out = filepath.Join(t.TempDir(), "failed-b.txt")
	calls := 0
	ac.diagnostic = func(ctx context.Context, dir, pin string, env gotool.Environment, bin string, args []string, diagnostics io.Writer) ([]byte, error) {
		calls++
		if calls == 2 {
			return nil, fmt.Errorf("side B diagnostic failed")
		}
		return run.ExecuteDiagnostic(ctx, dir, pin, env, bin, args, diagnostics)
	}
	stdout.Reset()
	stderr.Reset()
	if err := runAB(t.Context(), &stdout, &stderr, ac, []string{"./p"}); err == nil || exitCode(err) != 1 || !strings.Contains(err.Error(), "side B diagnostic failed") {
		t.Fatalf("failure hidden: %v", err)
	}
	retained, err := os.ReadFile(ac.out)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Count(retained, []byte("pew-ab-section: profile\n")) != 1 || bytes.Count(retained, []byte("BenchmarkWork-")) != 4 {
		t.Fatalf("completed units lost: %s", retained)
	}
	if !strings.Contains(stdout.String(), "side B diagnostic failed") || !strings.Contains(stdout.String(), `"integrity":"verified"`) {
		t.Fatalf("side dispositions lost: %s", &stdout)
	}
}

func TestABCapturedIncompatibleChildrenAreUnfulfilled(t *testing.T) {
	if testing.Short() {
		t.Skip("real independently captured allocation profiles")
	}
	root := abFixtureRepo(t)
	source := `package p
import "testing"
var sink []byte
//gofresh:pure
func BenchmarkWork(b *testing.B) { b.Run("base",func(b *testing.B){for i:=0;i<b.N;i++ {sink=make([]byte,1<<20)}}) }
`
	writeFile(t, filepath.Join(root, "p", "p_test.go"), source)
	repo, err := git.PlainOpen(root)
	if err != nil {
		t.Fatal(err)
	}
	commitAll(t, repo, "base child")
	writeFile(t, filepath.Join(root, "p", "p_test.go"), strings.Replace(source, `"base"`, `"new"`, 1))
	withWorkingDir(t, root)
	ac := abConfig{bench: "^BenchmarkWork$", count: 1, benchtime: "1x", ref: "HEAD", profile: "alloc", profileBenchtime: "30x", jsonOut: true, out: filepath.Join(t.TempDir(), "ab.txt")}
	var out, diagnostics bytes.Buffer
	err = runAB(t.Context(), &out, &diagnostics, ac, []string{"./p"})
	if err == nil || exitCode(err) != 2 {
		t.Fatalf("captured incompatible diagnostics: %v\n%s\n%s", err, &out, &diagnostics)
	}
	reports, _ := decodeProfileReport(t, out.Bytes())
	if len(reports) != 1 {
		t.Fatalf("%s", &out)
	}
	r := reports[0]
	if r.Compared || !strings.Contains(r.Reason, "children") || r.Base.Integrity != "verified" || r.New.Integrity != "verified" || r.Base.Error != "" || r.New.Error != "" {
		t.Fatalf("capture success confused with comparison eligibility: %+v", r)
	}
	artifact, err := os.ReadFile(ac.out)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Count(artifact, []byte("pew-ab-section: profile\n")) != 2 || bytes.Count(artifact, []byte("pew-ab-section: measurement\n")) != 2 {
		t.Fatalf("completed evidence lost: %s", artifact)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	captures := 0
	ac.diagnostic = func(c context.Context, dir, pin string, env gotool.Environment, bin string, args []string, diagnostics io.Writer) ([]byte, error) {
		data, err := run.ExecuteDiagnostic(c, dir, pin, env, bin, args, diagnostics)
		captures++
		if captures == 2 {
			cancel()
		}
		return data, err
	}
	out.Reset()
	diagnostics.Reset()
	err = runAB(ctx, &out, &diagnostics, ac, []string{"./p"})
	if err == nil || exitCode(err) != 130 {
		t.Fatalf("incompatibility hid interruption: %v\n%s", err, &out)
	}
	reports, _ = decodeProfileReport(t, out.Bytes())
	if len(reports) != 1 || reports[0].Base.Integrity != "verified" || reports[0].New.Integrity != "verified" {
		t.Fatalf("completed captures lost on signal: %s", &out)
	}
}
