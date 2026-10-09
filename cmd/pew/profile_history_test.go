package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	git "github.com/go-git/go-git/v5"
	pprof "github.com/google/pprof/profile"
	"github.com/greatliontech/pew/internal/compare"
	"github.com/greatliontech/pew/internal/gitblob"
	"github.com/greatliontech/pew/internal/profiles"
	"github.com/greatliontech/pew/internal/recordingtest"
	"github.com/greatliontech/pew/internal/run"
	"github.com/greatliontech/pew/internal/store"
	"golang.org/x/perf/benchfmt"
)

func TestHistoricalProfilesUseOnlyTheirRefObjects(t *testing.T) {
	root := abFixtureRepo(t)
	repo, err := git.PlainOpen(root)
	if err != nil {
		t.Fatal(err)
	}
	st := store.New(filepath.Join(root, "benchmarks"))
	ledger, err := run.EncodeLedger(run.Ledger{})
	if err != nil {
		t.Fatal(err)
	}
	var blob bytes.Buffer
	p := &pprof.Profile{SampleType: []*pprof.ValueType{{Type: "cpu", Unit: "nanoseconds"}}, Sample: []*pprof.Sample{{Value: []int64{13}}}}
	if err := p.Write(&blob); err != nil {
		t.Fatal(err)
	}
	hash, err := st.PutObject(t.Context(), blob.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	write := func(closure string) string {
		value := 1.0
		if closure == "source-b" {
			value = 100
		}
		values := make([]float64, 10)
		for i := range values {
			values[i] = value
		}
		rows := recordingtest.Results("Work", values, func(r *recordingtest.Recording) { r.Fingerprint.MaximalClosure = closure })
		rows = withConfig(rows, benchfmt.Config{Key: "pkg", Value: []byte("example.com/abfix/p"), File: true})
		fp, err := run.RecordedFingerprint(rows[0].Config)
		if err != nil {
			t.Fatal(err)
		}
		c := profiles.Capture{Kind: "cpu", Package: "example.com/abfix/p", Benchmark: "BenchmarkWork", Selection: "^BenchmarkWork$", Children: []profiles.Child{{Name: string(rows[0].Name), Iterations: 1}}, Budget: "1s", Protocol: profiles.Protocol, Scope: profiles.Scope, Sampling: "runtime-default", Commit: "source", Conditions: recordingtest.QuietConditions, BinarySHA256: profiles.Digest([]byte(closure)), MeasurementRevision: profiles.Revision(rows), Fingerprint: fp, Ledger: ledger, SHA256: hash, Size: int64(blob.Len()), Sources: []profiles.Source{{Filename: "/never/read/source.go", Bytes: []byte(closure)}}}
		encoded, err := profiles.Encode(profiles.Index{Version: 1, Captures: []profiles.Capture{c}})
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			r.Config = append(r.Config, run.KeyProfiles.Config(encoded))
		}
		if err := st.Write("p", "BenchmarkWork", "", rows); err != nil {
			t.Fatal(err)
		}
		return commitAll(t, repo, closure).String()
	}
	base, newer := write("source-a"), write("source-b")
	objects, err := gitblob.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := objects.SnapshotRefs("HEAD")
	if err != nil {
		t.Fatal(err)
	}
	path, _ := st.ObjectPath(hash)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	missing := commitAll(t, repo, "object removed").String()
	if b, ok, err := frozen.ReadAt("HEAD", path); err != nil || !ok || !bytes.Equal(b, blob.Bytes()) {
		t.Fatalf("moving ref changed frozen object: %v %v", ok, err)
	}
	if _, ok, err := objects.ReadAt("HEAD", path); err != nil || ok {
		t.Fatalf("fixture did not move HEAD: %v %v", ok, err)
	}
	check := func(ref string) profileComparison {
		m := &statModule{modulePath: "example.com/abfix", moduleDir: root, benchDir: st.Root, store: st, repo: objects}
		key := statKey{pkgRel: "p", bench: "BenchmarkWork"}
		for _, r := range []string{base, ref} {
			if _, ok, err := m.readSide(r, "p", "BenchmarkWork", ""); err != nil || !ok {
				t.Fatalf("read %s: %v", r, err)
			}
		}
		bl := baseline{baseRef: base, newRef: ref}
		resolved, err := m.resolveSubjects(key, bl)
		if err != nil {
			t.Fatal(err)
		}
		return compareStatProfiles(t.Context(), m, key, bl, []string{"cpu"}, resolved, newStatAnalysis(testEnvironment(t, nil)))[0]
	}
	if got := check(newer); !got.Compared || got.New.Freshness != "not-applicable" || got.Differences[0].NewTotal != 13 {
		t.Fatalf("historical object not read: %+v", got)
	}
	if _, err := st.PutObject(t.Context(), blob.Bytes()); err != nil {
		t.Fatal(err)
	}
	if got := check(missing); got.Compared || !strings.Contains(got.New.Error, "absent at") {
		t.Fatalf("working-tree object substituted: %+v", got)
	}
	withWorkingDir(t, root)
	var out, errout bytes.Buffer
	err = runStat(t.Context(), &out, &errout, statConfig{profile: "cpu", jsonOut: true, opts: compare.DefaultOptions()}, []string{base, missing})
	if exitCode(err) != 2 {
		t.Fatalf("missing requested diagnostic exit: %v\n%s", err, &out)
	}
	found := false
	decoder := json.NewDecoder(&out)
	for decoder.More() {
		var report profileComparison
		if err := decoder.Decode(&report); err != nil {
			t.Fatal(err)
		}
		if report.Kind == "profile" {
			found = true
			if report.Compared || report.New.Error == "" {
				t.Fatal("JSON hid missing object")
			}
		}
	}
	if !found {
		t.Fatal("no typed profile disposition")
	}
	out.Reset()
	err = runStat(t.Context(), &out, &errout, statConfig{profile: "cpu", jsonOut: true, failOnRegression: true, opts: compare.DefaultOptions()}, []string{base, missing})
	if exitCode(err) != 1 || err == nil || !strings.Contains(err.Error(), "regression detected") {
		t.Fatalf("profile refusal hid statistical regression: %v\n%s", err, &out)
	}
}
