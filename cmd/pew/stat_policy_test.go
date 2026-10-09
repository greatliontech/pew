package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	gogit "github.com/go-git/go-git/v5"
	"github.com/greatliontech/pew/internal/compare"
	"github.com/greatliontech/pew/internal/recordingtest"
	"github.com/greatliontech/pew/internal/run"
	"github.com/greatliontech/pew/internal/store"
)

func TestStatPolicyFlagsRefuseBeforeListing(t *testing.T) {
	withWorkingDir(t, t.TempDir())
	for _, flag := range []string{"coverage", "freshness", "conditions"} {
		cmd := newRootCmd()
		cmd.SetArgs([]string{"stat", "--" + flag + "=invalid"})
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "--"+flag+" must be") {
			t.Fatalf("%s: %v", flag, err)
		}
	}
}

func policyRepo(t *testing.T) (string, *gogit.Repository, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/policies\n\ngo 1.26.4\n")
	writeFile(t, filepath.Join(dir, "pkg", "pkg.go"), "package pkg\n")
	repo, err := gogit.PlainInit(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	return dir, repo, store.New(filepath.Join(dir, "benchmarks"))
}

func decodedPolicyReport(t *testing.T, text string) ([]compare.Disposition, compare.Coverage, []string) {
	t.Helper()
	var ds []compare.Disposition
	var cov compare.Coverage
	var notes []string
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		var n struct {
			Kind, Code, Text string
			Details          json.RawMessage
		}
		if err := json.Unmarshal([]byte(line), &n); err != nil {
			t.Fatalf("JSON: %v: %s", err, line)
		}
		switch n.Code {
		case "disposition":
			var d compare.Disposition
			if err := json.Unmarshal(n.Details, &d); err != nil {
				t.Fatal(err)
			}
			ds = append(ds, d)
		case "coverage":
			if err := json.Unmarshal(n.Details, &cov); err != nil {
				t.Fatal(err)
			}
		}
		if n.Kind == "note" {
			notes = append(notes, n.Text)
		}
	}
	return ds, cov, notes
}

func TestStatCLIPoliciesAndSemanticParity(t *testing.T) {
	dir, repo, st := policyRepo(t)
	writeStatRecording(t, st, "pkg", "BenchmarkGood", 100)
	writeStatRecording(t, st, "pkg", "BenchmarkGone", 100)
	base := commitAll(t, repo, "base").String()
	path, _ := st.Path("pkg", "BenchmarkGone", "")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	writeStatRecording(t, st, "pkg", "BenchmarkNew", 100)
	partial := commitAll(t, repo, "partial").String()
	writeStatRecording(t, st, "pkg", "BenchmarkGood", 130)
	regressed := commitAll(t, repo, "regressed").String()
	withWorkingDir(t, dir)
	for _, tc := range []struct {
		name, ref string
		flags     []string
		exit      int
	}{
		{"partial default", partial, nil, 0},
		{"complete", partial, []string{"--coverage=complete"}, 2},
		{"absent both unit", partial, []string{"--gate=sec/op,allocs/op", "--coverage=complete"}, 2},
		{"regression precedence", regressed, []string{"--coverage=complete"}, 1},
		{"historical freshness", partial, []string{"--freshness=require"}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var outputs [2]string
			for mode := 0; mode < 2; mode++ {
				args := append([]string{"stat", base, tc.ref, "--fail-on-regression"}, tc.flags...)
				if mode == 1 {
					args = append(args, "--json")
				}
				cmd := newRootCmd()
				cmd.SetArgs(args)
				var out, errOut bytes.Buffer
				cmd.SetOut(&out)
				cmd.SetErr(&errOut)
				err := cmd.Execute()
				code := 0
				if err != nil {
					code = exitCode(err)
				}
				if code != tc.exit {
					t.Fatalf("%v: exit %d (%v), want %d\n%s\n%s", args, code, err, tc.exit, out.String(), errOut.String())
				}
				outputs[mode] = out.String()
			}
			ds, cov, notes := decodedPolicyReport(t, outputs[1])
			if len(ds) == 0 || cov.Requested == 0 {
				t.Fatal("missing coverage")
			}
			for _, note := range notes {
				if !strings.Contains(outputs[0], note) {
					t.Fatalf("JSON-only semantic note %q\n%s", note, outputs[0])
				}
			}
			if !strings.Contains(outputs[0], "only present in base") || !strings.Contains(outputs[0], "only present in new") {
				t.Fatal("lost one-sided identities")
			}
		})
	}
}

func TestStatDeclaredUnrecordedAndFreshnessEligibility(t *testing.T) {
	dir, repo, st := policyRepo(t)
	writeFile(t, filepath.Join(dir, "pkg", "pkg_test.go"), "package pkg\nimport \"testing\"\nfunc BenchmarkUnrecorded(b *testing.B) {}\n")
	// The recorded benchmark has no declaration, so checking it is unavailable;
	// the genuinely declared benchmark must remain in the requested universe.
	writeStatRecording(t, st, "pkg", "BenchmarkStored", 100)
	base := commitAll(t, repo, "base").String()
	withWorkingDir(t, dir)
	for _, p := range []compare.Policies{{}, {Coverage: "complete"}, {Freshness: "require"}} {
		o := compare.DefaultOptions()
		o.Policies = p
		var out, errOut bytes.Buffer
		err := runStat(context.Background(), &out, &errOut, statConfig{opts: o, jsonOut: true, failOnRegression: true}, nil)
		if p == (compare.Policies{}) {
			if err != nil {
				t.Fatal(err)
			}
		} else {
			var empty *nothingComparedError
			if !errors.As(err, &empty) {
				t.Fatalf("policy %+v: %v", p, err)
			}
		}
		ds, cov, _ := decodedPolicyReport(t, out.String())
		if cov.Requested != 2 {
			t.Fatalf("lost declaration: %+v %+v", cov, ds)
		}
		found := false
		for _, d := range ds {
			if d.Recording == "BenchmarkUnrecorded" && len(d.Causes) > 0 && d.Causes[0].Side == "both" {
				found = true
			}
		}
		if !found {
			t.Fatalf("unrecorded declaration absent: %+v", ds)
		}
	}
	// Ref/ref coverage is about the recording trees, not today's declarations.
	var out bytes.Buffer
	o := compare.DefaultOptions()
	o.Policies.Coverage = "complete"
	if err := runStat(context.Background(), &out, io.Discard, statConfig{opts: o, failOnRegression: true}, []string{base, base}); err != nil {
		t.Fatal(err)
	}
}

func TestStatBlockedSidesRetainAllCausesAndChildIdentity(t *testing.T) {
	dir, repo, st := policyRepo(t)
	rows := recordingtest.Results("BenchmarkX", []float64{1, 1, 1, 1, 1, 1, 1, 1})
	for _, r := range rows {
		r.Name = []byte("X/child-8")
		for i := range r.Config {
			if r.Config[i].Key == run.KeyDirty.Name {
				r.Config[i].Value = []byte("true")
			}
		}
	}
	if err := st.Write("pkg", "BenchmarkX", "", rows); err != nil {
		t.Fatal(err)
	}
	base := commitAll(t, repo, "dirty base").String()
	path, _ := st.Path("pkg", "BenchmarkX", "")
	writeFile(t, path, "pew-format: 3\nBenchmarkX/child-8 1 1 sec/op\n")
	newer := commitAll(t, repo, "bad format").String()
	withWorkingDir(t, dir)
	var out bytes.Buffer
	if err := runStat(context.Background(), &out, io.Discard, statConfig{opts: compare.DefaultOptions(), jsonOut: true}, []string{base, newer}); err != nil {
		t.Fatal(err)
	}
	ds, c, _ := decodedPolicyReport(t, out.String())
	if c.Eligible != 0 || len(ds) != 1 {
		t.Fatalf("%+v %+v", c, ds)
	}
	d := ds[0]
	if d.Benchmark != "X/child-8" || len(d.Causes) < 2 || d.Causes[0].Code != "format" || d.Causes[1].Code != "dirty-ref" {
		t.Fatalf("lost/incorrect precedence %+v", d)
	}
}

func TestStatConditionsAndNumericRefusalsThroughCLI(t *testing.T) {
	dir, repo, st := policyRepo(t)
	writeStatRecording(t, st, "pkg", "BenchmarkX", 100)
	base := commitAll(t, repo, "base").String()
	rows, err := st.Read("pkg", "BenchmarkX", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		for i := range r.Config {
			if r.Config[i].Key == run.KeyRunConditions.Name {
				r.Config[i].Value = []byte("governor=unknown turbo=off throttled=false battery=false")
			}
		}
	}
	if err := st.Write("pkg", "BenchmarkX", "", rows); err != nil {
		t.Fatal(err)
	}
	unknown := commitAll(t, repo, "unknown").String()
	rows[0].Values[0].Value = -1
	if err := st.Write("pkg", "BenchmarkX", "", rows); err != nil {
		t.Fatal(err)
	}
	invalid := commitAll(t, repo, "invalid sample").String()
	withWorkingDir(t, dir)
	for _, tc := range []struct {
		ref, condition string
		rows, exit     int
	}{{unknown, "report", 1, 0}, {unknown, "compatible", 1, 2}, {invalid, "report", 0, 2}} {
		cmd := newRootCmd()
		cmd.SetArgs([]string{"stat", base, tc.ref, "--conditions=" + tc.condition, "--fail-on-regression", "--json"})
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(io.Discard)
		err := cmd.Execute()
		code := 0
		if err != nil {
			code = exitCode(err)
		}
		if code != tc.exit || strings.Count(out.String(), `"kind":"row"`) != tc.rows {
			t.Fatalf("%+v: %v\n%s", tc, err, out.String())
		}
	}
}

func TestStatSignalPrecedesIncompleteGate(t *testing.T) {
	err := errors.Join(&nothingComparedError{reason: "incomplete"}, interrupted("stopped"))
	if exitCode(err) != 130 {
		t.Fatal("gate outranks interruption")
	}
}

func TestStatRecordingIdentityDoesNotDependOnPkgMetadata(t *testing.T) {
	dir, repo, st := policyRepo(t)
	for _, pkg := range []string{"one", "two"} {
		writeStatRecording(t, st, pkg, "BenchmarkSame", 100)
	}
	base := commitAll(t, repo, "base").String()
	writeStatRecording(t, st, "one", "BenchmarkSame", 100)
	path, _ := st.Path("two", "BenchmarkSame", "")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	newer := commitAll(t, repo, "missing second package").String()
	withWorkingDir(t, dir)
	o := compare.DefaultOptions()
	o.Policies.Coverage = "complete"
	var out bytes.Buffer
	err := runStat(context.Background(), &out, io.Discard, statConfig{opts: o, jsonOut: true, failOnRegression: true}, []string{base, newer})
	var missing *nothingComparedError
	if !errors.As(err, &missing) {
		t.Fatalf("merged package evidence: %v\n%s", err, out.String())
	}
	ds, c, _ := decodedPolicyReport(t, out.String())
	if c.Requested != 2 || c.Compared != 1 || c.Missing != 1 {
		t.Fatalf("%+v %+v", c, ds)
	}
	if ds[0].Package == ds[1].Package {
		t.Fatal("lost recording package identity")
	}
}

func TestStatUnknownCurrentInventoryCannotClaimComplete(t *testing.T) {
	dir, repo, st := policyRepo(t)
	writeStatRecording(t, st, "pkg", "BenchmarkStored", 100)
	base := commitAll(t, repo, "base").String()
	writeFile(t, filepath.Join(dir, "pkg", "broken_test.go"), "package pkg\nfunc BenchmarkBroken(\n")
	withWorkingDir(t, dir)
	for _, mode := range []string{"partial", "complete"} {
		o := compare.DefaultOptions()
		o.Policies.Coverage = mode
		var out bytes.Buffer
		err := runStat(context.Background(), &out, io.Discard, statConfig{opts: o, jsonOut: true, failOnRegression: true}, nil)
		if mode == "partial" && err != nil {
			t.Fatal(err)
		}
		if mode == "complete" {
			var empty *nothingComparedError
			if !errors.As(err, &empty) {
				t.Fatalf("unproven inventory passed: %v", err)
			}
		}
		_, c, _ := decodedPolicyReport(t, out.String())
		if c.InventoryComplete || c.Compared != 1 {
			t.Fatalf("inventory: %+v\n%s", c, out.String())
		}
	}
	o := compare.DefaultOptions()
	o.Policies.Coverage = "complete"
	if err := runStat(context.Background(), io.Discard, io.Discard, statConfig{opts: o, failOnRegression: true}, []string{base, base}); err != nil {
		t.Fatalf("current parse failure blocked historical scope: %v", err)
	}
}

func TestStatCoverageRespectsLabelScope(t *testing.T) {
	dir, repo, st := policyRepo(t)
	rows := recordingtest.Results("BenchmarkX", []float64{1, 1, 1, 1, 1, 1, 1, 1})
	if err := st.Write("pkg", "BenchmarkX", "fast", rows); err != nil {
		t.Fatal(err)
	}
	writeStatRecording(t, st, "pkg", "BenchmarkUnselected", 100)
	base := commitAll(t, repo, "base").String()
	path, _ := st.Path("pkg", "BenchmarkUnselected", "")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	newer := commitAll(t, repo, "unselected removed").String()
	withWorkingDir(t, dir)
	o := compare.DefaultOptions()
	o.Policies.Coverage = "complete"
	var out bytes.Buffer
	if err := runStat(context.Background(), &out, io.Discard, statConfig{opts: o, label: "fast", jsonOut: true, failOnRegression: true}, []string{base, newer}); err != nil {
		t.Fatal(err)
	}
	ds, c, _ := decodedPolicyReport(t, out.String())
	if c.Requested != 1 || len(ds) != 1 || ds[0].Label != "fast" {
		t.Fatalf("label scope %+v %+v", c, ds)
	}
}

func TestStatExecutableExitAndJSON(t *testing.T) {
	if testing.Short() {
		t.Skip("builds the real CLI executable")
	}
	binary := filepath.Join(t.TempDir(), "pew")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", binary, ".")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	dir, repo, st := policyRepo(t)
	writeStatRecording(t, st, "pkg", "BenchmarkX", 100)
	base := commitAll(t, repo, "base").String()
	writeStatRecording(t, st, "pkg", "BenchmarkNew", 100)
	newer := commitAll(t, repo, "newer").String()
	var reports [][]compare.Disposition
	for _, coverage := range []string{"partial", "complete"} {
		cmd := exec.CommandContext(t.Context(), binary, "stat", base, newer, "--json", "--fail-on-regression", "--coverage="+coverage)
		cmd.Dir = dir
		var out, errOut bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &errOut
		err := cmd.Run()
		want := 0
		if coverage == "complete" {
			want = 2
		}
		got := 0
		if err != nil {
			var ee *exec.ExitError
			if !errors.As(err, &ee) {
				t.Fatal(err)
			}
			got = ee.ExitCode()
		}
		if got != want {
			t.Fatalf("%s: %v %s", coverage, err, errOut.String())
		}
		ds, _, _ := decodedPolicyReport(t, out.String())
		reports = append(reports, ds)
	}
	if !reflect.DeepEqual(reports[0], reports[1]) {
		t.Fatal("coverage policy changed the evidence")
	}
}

func TestStatFreshnessPolicyUsesSharedValidAndStaleJudgments(t *testing.T) {
	if testing.Short() {
		t.Skip("records and judges a fixture through the shared engine")
	}
	dir := twoArmFixture(t)
	withWorkingDir(t, dir)
	source := "package arms\nimport \"testing\"\n//gofresh:pure\nfunc BenchmarkFirst(b *testing.B) {}\n//gofresh:pure\nfunc BenchmarkSecond(b *testing.B) {}\n"
	writeFile(t, filepath.Join(dir, "bench_test.go"), source)
	repo, err := gogit.PlainOpen(dir)
	if err != nil {
		t.Fatal(err)
	}
	commitAll(t, repo, "pure benchmarks")
	rc := runConfig{opts: run.Options{Count: 1, Benchtime: "1x", Bench: "."}, execute: armStub(nil)}
	var recorded, diagnostics bytes.Buffer
	if err := runRun(context.Background(), &recorded, &diagnostics, rc, []string{"."}); err != nil {
		t.Fatalf("record: %v\n%s\n%s", err, recorded.String(), diagnostics.String())
	}
	commitAll(t, repo, "recordings")
	for _, state := range []string{"valid", "stale"} {
		if state == "stale" {
			writeFile(t, filepath.Join(dir, "bench_test.go"), strings.Replace(source, "BenchmarkFirst(b *testing.B) {}", "BenchmarkFirst(b *testing.B) { b.SetBytes(2) }", 1))
		}
		for _, policy := range []string{"report", "require"} {
			cmd := newRootCmd()
			cmd.SetArgs([]string{"stat", "--freshness=" + policy, "--fail-on-regression", "--json"})
			var out, errOut bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&errOut)
			err := cmd.Execute()
			want := 0
			if state == "stale" && policy == "require" {
				want = 2
			}
			got := 0
			if err != nil {
				got = exitCode(err)
			}
			if got != want {
				t.Fatalf("%s %s: %v\n%s\n%s", state, policy, err, out.String(), errOut.String())
			}
			ds, c, _ := decodedPolicyReport(t, out.String())
			if c.Compared != 2 {
				t.Fatalf("policy hid rows: %+v", c)
			}
			for _, d := range ds {
				if d.Freshness != state {
					t.Fatalf("shared judgment lost: %+v", d)
				}
			}
		}
	}
}
