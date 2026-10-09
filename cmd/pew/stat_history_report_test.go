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

	"github.com/greatliontech/pew/internal/compare"
	"github.com/greatliontech/pew/internal/recordingtest"
	"github.com/greatliontech/pew/internal/run"
	"golang.org/x/perf/benchfmt"
)

func historicalRows(pkg string, opts ...recordingtest.Option) []*benchfmt.Result {
	rows := recordingtest.Results("X/child-8", []float64{1, 1, 1, 1, 1, 1, 1, 1}, opts...)
	if pkg != "" {
		for _, r := range rows {
			r.Config = append(r.Config, benchfmt.Config{Key: "pkg", Value: []byte(pkg), File: true})
		}
	}
	return rows
}

func TestStatHistoricalSubjectsIgnoreCurrentModuleRename(t *testing.T) {
	for _, withPkg := range []bool{false, true} {
		for _, dirty := range []bool{false, true} {
			t.Run(strings.Join([]string{boolName(withPkg), boolName(dirty)}, "/"), func(t *testing.T) {
				dir, repo, st := policyRepo(t)
				writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/oldmodule\n\ngo 1.26.4\n")
				pkg := ""
				if withPkg {
					pkg = "example.com/oldmodule/pkg"
				}
				if err := st.Write("pkg", "BenchmarkX", "", historicalRows(pkg)); err != nil {
					t.Fatal(err)
				}
				a := commitAll(t, repo, "historical base").String()
				opts := []recordingtest.Option{recordingtest.Set(run.KeyCommit, "c2")}
				if dirty {
					opts = append(opts, recordingtest.Set(run.KeyDirty, "true"))
				}
				if err := st.Write("pkg", "BenchmarkX", "", historicalRows(pkg, opts...)); err != nil {
					t.Fatal(err)
				}
				b := commitAll(t, repo, "historical new").String()
				writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/newmodule\n\ngo 1.26.4\n")
				withWorkingDir(t, dir)
				for _, machine := range []bool{false, true} {
					var out bytes.Buffer
					if err := runStat(context.Background(), &out, io.Discard, statConfig{opts: compare.DefaultOptions(), jsonOut: machine}, []string{a, b}); err != nil {
						t.Fatal(err)
					}
					if strings.Contains(out.String(), "example.com/newmodule") || !strings.Contains(out.String(), "example.com/oldmodule/pkg") {
						t.Fatalf("historical subject borrowed current context:\n%s", out.String())
					}
					if machine {
						ds, c, _ := decodedPolicyReport(t, out.String())
						if len(ds) != 1 || c.Requested != 1 || ds[0].Package != "example.com/oldmodule/pkg" || ds[0].BasePackage != ds[0].Package || ds[0].NewPackage != ds[0].Package {
							t.Fatalf("subjects %+v %+v", ds, c)
						}
						if ds[0].Compared == dirty {
							t.Fatalf("dirty disposition %+v", ds[0])
						}
						for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
							var r struct{ Kind, Package, BasePackage, NewPackage string }
							if err := json.Unmarshal([]byte(line), &r); err != nil {
								t.Fatal(err)
							}
							if r.Kind == "row" && (r.Package != "example.com/oldmodule/pkg" || r.BasePackage != r.Package || r.NewPackage != r.Package) {
								t.Fatalf("row subject %+v", r)
							}
						}
					}
				}
			})
		}
	}
}

func boolName(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func TestStatRenamePairingRetainsSideContextsAndConfigurations(t *testing.T) {
	for _, changedPkg := range []bool{false, true} {
		t.Run(boolName(changedPkg), func(t *testing.T) {
			dir, repo, st := policyRepo(t)
			writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/oldmodule\n\ngo 1.26.4\n")
			pkg := ""
			if changedPkg {
				pkg = "example.com/oldmodule/pkg"
			}
			if err := st.Write("pkg", "BenchmarkX", "", historicalRows(pkg)); err != nil {
				t.Fatal(err)
			}
			a := commitAll(t, repo, "old module").String()
			writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/newmodule\n\ngo 1.26.4\n")
			if changedPkg {
				if err := st.Write("pkg", "BenchmarkX", "", historicalRows("example.com/newmodule/pkg")); err != nil {
					t.Fatal(err)
				}
			}
			b := commitAll(t, repo, "new module").String()
			withWorkingDir(t, dir)
			var out bytes.Buffer
			if err := runStat(context.Background(), &out, io.Discard, statConfig{opts: compare.DefaultOptions(), jsonOut: true}, []string{a, b}); err != nil {
				t.Fatal(err)
			}
			ds, c, _ := decodedPolicyReport(t, out.String())
			if changedPkg {
				if c.Requested != 2 || c.Compared != 0 {
					t.Fatalf("guessed a cross-config rename: %+v", c)
				}
			} else {
				if c.Requested != 1 || c.Compared != 1 || len(ds) != 1 || ds[0].Package != "" || ds[0].BasePackage != "example.com/oldmodule/pkg" || ds[0].NewPackage != "example.com/newmodule/pkg" {
					t.Fatalf("same-path pairing lost side identities: %+v %+v", c, ds)
				}
			}
			if !strings.Contains(out.String(), "example.com/oldmodule/pkg") || !strings.Contains(out.String(), "example.com/newmodule/pkg") {
				t.Fatal("missing a side's context")
			}
		})
	}
}

func TestStatBlockedAuditNotesHaveTextJSONParity(t *testing.T) {
	for _, blocker := range []string{"dirty", "strategy", "format"} {
		for _, shape := range []string{"matched", "disjoint-child", "disjoint-config", "matched-children"} {
			t.Run(blocker+"/"+shape, func(t *testing.T) {
				dir, repo, st := policyRepo(t)
				rows := func(newer bool, opts ...recordingtest.Option) []*benchfmt.Result {
					rs := historicalRows("", opts...)
					if shape == "disjoint-child" {
						name := "X/old-8"
						if newer {
							name = "X/new-8"
						}
						for _, r := range rs {
							r.Name = []byte(name)
						}
					}
					if shape == "disjoint-config" {
						cpu := "base-cpu"
						if newer {
							cpu = "new-cpu"
						}
						for _, r := range rs {
							r.Config = append(r.Config, benchfmt.Config{Key: "cpu", Value: []byte(cpu), File: true})
						}
					}
					if shape == "matched-children" {
						more := historicalRows("", opts...)
						for _, r := range more {
							r.Name = []byte("X/sibling-8")
						}
						rs = append(rs, more...)
					}
					return rs
				}
				base := rows(false, recordingtest.Set(run.KeyVouches, "example.com/dep.Base"), recordingtest.Set(run.KeyClosureStrategy, "base-derivation"))
				if err := st.Write("pkg", "BenchmarkX", "", base); err != nil {
					t.Fatal(err)
				}
				a := commitAll(t, repo, "base").String()
				opts := []recordingtest.Option{recordingtest.Set(run.KeyVouches, "example.com/dep.New"), recordingtest.Set(run.KeyClosureStrategy, "new-derivation")}
				if blocker == "dirty" {
					opts = append(opts, recordingtest.Set(run.KeyDirty, "true"))
				}
				if blocker == "strategy" {
					opts = append(opts, recordingtest.Set(run.KeyDynamicState, "older-strategy"))
				}
				if err := st.Write("pkg", "BenchmarkX", "", rows(true, opts...)); err != nil {
					t.Fatal(err)
				}
				if blocker == "format" {
					path, err := st.Path("pkg", "BenchmarkX", "")
					if err != nil {
						t.Fatal(err)
					}
					data, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					writeFile(t, path, strings.Replace(string(data), "pew-format: "+run.RecordingFormat, "pew-format: 3", 1))
				}
				refs := []string{a}
				if blocker != "strategy" {
					refs = append(refs, commitAll(t, repo, "blocked historical").String())
				}
				withWorkingDir(t, dir)
				var text, jsonOut bytes.Buffer
				for _, machine := range []bool{false, true} {
					var out io.Writer = &text
					if machine {
						out = &jsonOut
					}
					err := runStat(context.Background(), out, io.Discard, statConfig{opts: compare.DefaultOptions(), jsonOut: machine, failOnRegression: true}, refs)
					var empty *nothingComparedError
					if !errors.As(err, &empty) {
						t.Fatalf("blocked gate: %v", err)
					}
				}
				ds, c, notes := decodedPolicyReport(t, jsonOut.String())
				want := 1
				if shape == "matched-children" || (shape != "matched" && blocker != "format") {
					want = 2
				}
				if len(ds) != want || c.Requested != want || c.Compared != 0 || c.Eligible != 0 || strings.Contains(jsonOut.String(), `"kind":"row"`) {
					t.Fatalf("audit changed denominator/evidence: %+v %+v", c, ds)
				}
				if blocker != "format" {
					for _, d := range ds {
						if d.Conditions != "compatible" {
							t.Fatalf("child mismatch fabricated missing recording conditions: %+v", d)
						}
					}
				}
				for _, note := range notes {
					if !strings.Contains(text.String(), note) {
						t.Fatalf("JSON-only note %q\n%s", note, text.String())
					}
				}
				for _, audit := range []string{"dynamic-state vouches differ", "closure derivations differ", "example.com/dep.Base", "example.com/dep.New", "base-derivation", "new-derivation"} {
					if got := strings.Contains(jsonOut.String(), audit); got != (blocker != "format") {
						t.Fatalf("%s audit %q present=%t:\n%s", blocker, audit, got, jsonOut.String())
					}
				}
				if blocker != "format" {
					for _, audit := range []string{"dynamic-state vouches differ (base: example.com/dep.Base; new: example.com/dep.New)", "closure derivations differ (base: base-derivation; new: new-derivation)"} {
						if strings.Count(text.String(), audit) != 1 || strings.Count(jsonOut.String(), audit) != 1 {
							t.Fatalf("audit missing, duplicated or misattributed: %q\ntext:\n%s\nJSON:\n%s", audit, text.String(), jsonOut.String())
						}
					}
				}
			})
		}
	}
}
