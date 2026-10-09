package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	git "github.com/go-git/go-git/v5"
	pprof "github.com/google/pprof/profile"
	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/pew/internal/compare"
	"github.com/greatliontech/pew/internal/profiles"
	"github.com/greatliontech/pew/internal/recordingtest"
	"github.com/greatliontech/pew/internal/run"
	"github.com/greatliontech/pew/internal/store"
)

func writeComparisonProfile(t *testing.T, st *store.Store, pkgRel, pkg, name string, damage string) {
	t.Helper()
	rows := recordingtest.Results(name, []float64{1, 2, 1, 2, 1, 2, 1, 2, 1, 2})
	// Intentionally omit stream pkg: ref-local context is independently required.
	var raw bytes.Buffer
	p := &pprof.Profile{SampleType: []*pprof.ValueType{{Type: "cpu", Unit: "nanoseconds"}}, Sample: []*pprof.Sample{{Value: []int64{13}}}}
	if err := p.Write(&raw); err != nil {
		t.Fatal(err)
	}
	if damage == "native" {
		raw.Reset()
		raw.WriteString("not a native profile")
	}
	hash, err := st.PutObject(t.Context(), raw.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	ledger, err := run.EncodeLedger(run.Ledger{})
	if err != nil {
		t.Fatal(err)
	}
	c := profiles.Capture{Kind: "cpu", Package: pkg, Benchmark: "Benchmark" + name, Selection: "^Benchmark" + name + "$", Children: []profiles.Child{{Name: string(rows[0].Name), Iterations: 1}}, Budget: "1s", Protocol: profiles.Protocol, Scope: profiles.Scope, Sampling: "runtime-default", Commit: "source", Conditions: recordingtest.QuietConditions, BinarySHA256: profiles.Digest([]byte("binary")), MeasurementRevision: profiles.Revision(rows), Fingerprint: recordingtest.Defaults().Fingerprint, Ledger: ledger, SHA256: hash, Size: int64(raw.Len())}
	encoded, err := profiles.Encode(profiles.Index{Version: 1, Captures: []profiles.Capture{c}})
	if err != nil {
		t.Fatal(err)
	}
	if damage == "index" {
		encoded = "broken"
	}
	for _, r := range rows {
		r.Config = append(r.Config, run.KeyProfiles.Config(encoded))
	}
	if err := st.Write(pkgRel, "Benchmark"+name, "", rows); err != nil {
		t.Fatal(err)
	}
	path, _ := st.ObjectPath(hash)
	switch damage {
	case "missing":
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	case "bytes":
		if err := os.WriteFile(path, []byte("corrupt bytes"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func decodeProfileReport(t *testing.T, data []byte) (reports []profileComparison, statistical int) {
	t.Helper()
	d := json.NewDecoder(bytes.NewReader(data))
	for {
		var raw json.RawMessage
		if err := d.Decode(&raw); err == io.EOF {
			break
		} else if err != nil {
			t.Fatal(err)
		}
		var r profileComparison
		if err := json.Unmarshal(raw, &r); err != nil {
			t.Fatal(err)
		}
		if r.Kind == "profile" {
			reports = append(reports, r)
		} else if r.Kind == "row" {
			statistical++
		}
	}
	return
}

func TestProfileFailureClassesKeepStatisticalReports(t *testing.T) {
	for _, damage := range []string{"missing", "bytes", "native", "index"} {
		t.Run(damage, func(t *testing.T) {
			root := abFixtureRepo(t)
			st := store.New(filepath.Join(root, "benchmarks"))
			writeComparisonProfile(t, st, "p", "example.com/abfix/p", "Work", damage)
			repo, err := git.PlainOpen(root)
			if err != nil {
				t.Fatal(err)
			}
			ref := commitAll(t, repo, "profile evidence").String()
			withWorkingDir(t, root)
			for _, jsonOut := range []bool{false, true} {
				var out, diagnostics bytes.Buffer
				err := runStat(t.Context(), &out, &diagnostics, statConfig{profile: "cpu", jsonOut: jsonOut, opts: compare.DefaultOptions()}, []string{ref, ref})
				want := 1
				if damage == "missing" {
					want = 2
				}
				if err == nil || exitCode(err) != want {
					t.Fatalf("%s json=%t exit=%v want=%d\n%s", damage, jsonOut, err, want, &out)
				}
				if jsonOut {
					reports, stats := decodeProfileReport(t, out.Bytes())
					if len(reports) != 1 || stats == 0 || reports[0].Compared || reports[0].Base.Error == "" || reports[0].New.Error == "" {
						t.Fatalf("lost independent reports: %s", &out)
					}
					if damage != "missing" && reports[0].Base.Integrity != "invalid" {
						t.Fatalf("integrity misclassified: %+v", reports[0])
					}
					if damage != "missing" && strings.Contains(reports[0].Base.Reason, "capture absent") {
						t.Fatalf("invalid evidence reported absent: %+v", reports[0])
					}
				} else if !strings.Contains(out.String(), "sec/op") || !strings.Contains(out.String(), "compared=false") {
					t.Fatalf("lost text reports: %s", &out)
				}
			}
		})
	}
}

func TestProfileRefLocalRootAndRenamedModuleContexts(t *testing.T) {
	root := abFixtureRepo(t)
	st := store.New(filepath.Join(root, "benchmarks"))
	repo, err := git.PlainOpen(root)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/old\ngo 1.24\n")
	writeComparisonProfile(t, st, "", "example.com/old", "Work", "")
	base := commitAll(t, repo, "old module").String()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/new\ngo 1.24\n")
	writeComparisonProfile(t, st, "", "example.com/new", "Work", "")
	newer := commitAll(t, repo, "renamed module").String()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/today\ngo 1.24\n")
	withWorkingDir(t, root)
	for _, rename := range []bool{false, true} {
		for _, jsonOut := range []bool{false, true} {
			ref := base
			if rename {
				ref = newer
			}
			var out bytes.Buffer
			err := runStat(t.Context(), &out, io.Discard, statConfig{profile: "cpu", jsonOut: jsonOut, opts: compare.DefaultOptions()}, []string{base, ref})
			if rename {
				if err == nil || exitCode(err) != 2 {
					t.Fatalf("renamed subject comparison: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if jsonOut {
				reports, _ := decodeProfileReport(t, out.Bytes())
				if len(reports) != 1 {
					t.Fatalf("%s", &out)
				}
				r := reports[0]
				wantNew := "example.com/old"
				wantPackage := wantNew
				if rename {
					wantNew = "example.com/new"
					wantPackage = ""
				}
				if r.Package != wantPackage || r.BasePackage != "example.com/old" || r.NewPackage != wantNew || r.Base.Package != "example.com/old" || r.New.Package != wantNew {
					t.Fatalf("wrong ref-local root identities: %+v", r)
				}
			} else {
				text := out.String()
				if !strings.Contains(text, "example.com/old.BenchmarkWork") || strings.Contains(text, "example.com/old/.BenchmarkWork") || strings.Contains(text, "example.com/today") {
					t.Fatalf("wrong text identity: %s", text)
				}
				if rename && !strings.Contains(text, "base=example.com/old new=example.com/new") {
					t.Fatalf("missing side contexts: %s", text)
				}
			}
		}
	}
}

func TestProfileReadAndCheckingFailuresBothRemainVisible(t *testing.T) {
	root := abFixtureRepo(t)
	st := store.New(filepath.Join(root, "benchmarks"))
	writeComparisonProfile(t, st, "p", "example.com/abfix/p", "Work", "")
	repo, err := git.PlainOpen(root)
	if err != nil {
		t.Fatal(err)
	}
	commitAll(t, repo, "valid historical profile")
	rows, err := st.Read("p", "BenchmarkWork", "")
	if err != nil {
		t.Fatal(err)
	}
	index, _, err := profiles.FromRows(rows)
	if err != nil {
		t.Fatal(err)
	}
	path, _ := st.ObjectPath(index.Captures[0].SHA256)
	if err := os.WriteFile(path, []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	withWorkingDir(t, root)
	deps := dependencies(t.Context())
	deps.view = func(*gofresh.Engine, context.Context, []gofresh.Subject, string, gofresh.Kind) (*gofresh.View, error) {
		return nil, errors.New("checking unavailable")
	}
	ctx := context.WithValue(t.Context(), dependenciesKey{}, deps)
	var out bytes.Buffer
	err = runStat(ctx, &out, io.Discard, statConfig{profile: "cpu", jsonOut: true, opts: compare.DefaultOptions()}, nil)
	if err == nil || exitCode(err) != 1 {
		t.Fatalf("operational failures: %v", err)
	}
	reports, stats := decodeProfileReport(t, out.Bytes())
	if len(reports) != 1 || stats == 0 || !strings.Contains(reports[0].New.Error, "integrity failure") || !strings.Contains(reports[0].New.Error, "checking unavailable") {
		t.Fatalf("a failure erased another: %s", &out)
	}
}

func TestStatProfilesSharePackageFactsWithIndependentChecks(t *testing.T) {
	if testing.Short() {
		t.Skip("native package source analysis")
	}
	root := twoArmFixture(t)
	st := store.New(filepath.Join(root, "benchmarks"))
	for _, name := range []string{"First", "Second"} {
		writeComparisonProfile(t, st, "", "example.com/arms", name, "")
	}
	repo, err := git.PlainOpen(root)
	if err != nil {
		t.Fatal(err)
	}
	commitAll(t, repo, "two profiles")
	withWorkingDir(t, root)
	deps := dependencies(t.Context())
	loads := 0
	deps.view = func(e *gofresh.Engine, ctx context.Context, subjects []gofresh.Subject, dir string, kind gofresh.Kind) (*gofresh.View, error) {
		loads++
		if len(subjects) != 2 {
			t.Fatalf("package preparation narrowed to one diagnostic: %v", subjects)
		}
		return e.NewViewFor(ctx, subjects, dir, kind)
	}
	ctx := context.WithValue(t.Context(), dependenciesKey{}, deps)
	var out bytes.Buffer
	if err := runStat(ctx, &out, io.Discard, statConfig{profile: "cpu", jsonOut: true, opts: compare.DefaultOptions()}, nil); err != nil {
		t.Fatalf("%v\n%s", err, &out)
	}
	reports, stats := decodeProfileReport(t, out.Bytes())
	if loads != 1 || len(reports) != 2 || stats != 2 {
		t.Fatalf("loads=%d profiles=%d statistics=%d", loads, len(reports), stats)
	}
	for _, r := range reports {
		if r.New.Integrity != "verified" || r.New.Freshness != "stale" || r.New.Error != "" {
			t.Fatalf("checking transactions lost: %+v", r)
		}
	}
}
