package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/pew/internal/recordingtest"
	runpkg "github.com/greatliontech/pew/internal/run"
	"github.com/spf13/cobra"
)

// The --vouch flags resolve through the engine's own grammar and set
// rule: the colon pair form parses and canonicalizes, a repeated entry
// is one acceptance, the set is sorted, and a bare package, control
// characters, or a non-identifier variable refuse the whole set loudly
// (spec §12 --vouch).
func TestResolveVouchesTakesTheEnginesSet(t *testing.T) {
	s, err := newVouchSource("", []string{"b.example/dep:Var", "a.example/dep:Var", "b.example/dep:Var"})
	if err != nil || len(s.flags) != 2 || s.flags[0] != "a.example/dep.Var" || s.flags[1] != "b.example/dep.Var" {
		t.Fatalf("resolved = %v, %v", s, err)
	}
	for _, bad := range []string{"a.example/dep", "", ":Var", "a.example/dep:", "a.example/dep:not-ident", "a.example/dep:9x", "a.example/dep:V.S", "a.example/dep :Var", "a.example/dep\x01x:Var"} {
		if _, err := newVouchSource("", []string{"a.example/dep:Var", bad}); err == nil {
			t.Fatalf("malformed vouch %q accepted", bad)
		}
	}
}

// Vouches cross the native payload boundary as audit evidence (spec §5).
func TestVouchesConfigRoundTrip(t *testing.T) {
	cfg := recordingtest.Config(recordingtest.Set(runpkg.KeyVouches, "a.example/dep.Var"))
	fp, _, ok := fingerprintFromConfig(cfg)
	if !ok || fp.DynamicStateVouches != "a.example/dep.Var" {
		t.Fatalf("round trip = %+v ok=%v", fp, ok)
	}
}

// The --vouch set reaches every engine the commands build: a real
// pinned-dependency culprit (protobuf's global registries) downgrades
// an importing benchmark's subject, and the vouched engine lifts
// exactly it, the discharge riding the fingerprint that run records
// (spec §12, §5 pew-vouches).
func TestVouchedEngineDischargesPinnedCulprit(t *testing.T) {
	if testing.Short() {
		t.Skip("builds gofresh views over the protobuf graph")
	}
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	out, err := exec.Command("go", "env", "GOMODCACHE").Output()
	if err != nil {
		t.Fatal(err)
	}
	cached, err := filepath.Glob(filepath.Join(strings.TrimSpace(string(out)), "google.golang.org", "protobuf@v*"))
	if err != nil || len(cached) == 0 {
		t.Skipf("google.golang.org/protobuf absent from the module cache: %v %v", cached, err)
	}
	sort.Strings(cached)
	version := cached[len(cached)-1][strings.LastIndex(cached[len(cached)-1], "@")+1:]
	dir := t.TempDir()
	for name, content := range map[string]string{
		"go.mod": "module example.com/vouchbench\n\ngo 1.26\n\nrequire google.golang.org/protobuf " + version + "\n",
		"reg.go": `package vouchbench

import (
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

func Count() int {
	n := 0
	protoregistry.GlobalFiles.RangeFiles(func(protoreflect.FileDescriptor) bool {
		n++
		return true
	})
	return n
}
`,
		"reg_test.go": `package vouchbench

import "testing"

func BenchmarkCount(b *testing.B) {
	for i := 0; i < b.N; i++ {
		Count()
	}
}
`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	tidy := exec.Command("go", "mod", "tidy")
	tidy.Dir = dir
	tidy.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOPROXY=off")
	if out, err := tidy.CombinedOutput(); err != nil {
		t.Fatalf("go mod tidy: %v\n%s", err, out)
	}
	ctx := context.Background()
	subject := gofresh.Subject{Package: "example.com/vouchbench", Symbol: "BenchmarkCount"}
	capture := func(vouches ...string) gofresh.Fingerprint {
		t.Helper()
		owned := context.WithValue(ctx, invocationKey{}, &invocation{vouches: &vouchSource{flags: vouches}})
		e, err := buildEngine(owned, dir, testEnvironment(t, os.Environ()), nil, "")
		if err != nil {
			t.Fatal(err)
		}
		view, err := e.NewView(ctx, []gofresh.Subject{subject}, dir)
		if err != nil {
			t.Fatal(err)
		}
		fp, err := view.Capture(ctx, subject)
		if err != nil {
			t.Fatal(err)
		}
		return fp
	}
	plainEngine, err := buildEngine(context.Background(), dir, testEnvironment(t, os.Environ()), nil, "")
	if err != nil {
		t.Fatal(err)
	}
	plainView, err := plainEngine.NewView(ctx, []gofresh.Subject{subject}, dir)
	if err != nil {
		t.Fatal(err)
	}
	plainFP, err := plainView.Capture(ctx, subject)
	if err != nil {
		t.Fatal(err)
	}
	verdict, err := plainView.Check(ctx, plainFP, subject)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(verdict.Reason, "shares mutated dynamic state") {
		t.Fatalf("plain verdict = %+v, want the downgrade", verdict)
	}
	m := regexp.MustCompile(`([^\s:]+): ([^\s:]+)\.([\p{L}_][\p{L}\p{Nd}_]*) `).FindStringSubmatch(verdict.Reason + " ")
	if m == nil || m[1] != m[2] {
		t.Fatalf("no culprit parsed from %q", verdict.Reason)
	}
	culprit := m[1] + "." + m[3]
	culpritEntry := m[1] + ":" + m[3] // the IMPORT-PATH:VARIABLE spelling

	fp := capture(culprit)
	if fp.DynamicStateVouches != culprit {
		t.Fatalf("vouched fingerprint discharge = %q, want %q", fp.DynamicStateVouches, culprit)
	}
	if plainFP.DynamicStateVouches != "" {
		t.Fatalf("plain fingerprint carries a discharge: %q", plainFP.DynamicStateVouches)
	}

	// The vouched VERDICT no longer names the culprit - the discharge is
	// load-bearing, not merely recorded.
	owned := context.WithValue(ctx, invocationKey{}, &invocation{vouches: &vouchSource{flags: []string{culprit}}})
	vouchedEngine, err := buildEngine(owned, dir, testEnvironment(t, os.Environ()), nil, "")
	if err != nil {
		t.Fatal(err)
	}
	vouchedView, err := vouchedEngine.NewView(ctx, []gofresh.Subject{subject}, dir)
	if err != nil {
		t.Fatal(err)
	}
	vouchedFP, err := vouchedView.Capture(ctx, subject)
	if err != nil {
		t.Fatal(err)
	}
	vouchedVerdict, err := vouchedView.Check(ctx, vouchedFP, subject)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(vouchedVerdict.Reason, culprit) {
		t.Fatalf("vouched verdict still names the culprit: %+v", vouchedVerdict)
	}

	// The store's reviewed vouch file is the standing set every engine
	// over the module judges under, with no flag given: the same
	// discharge, from the file beside the recordings (REQ-pew-vouch-source).
	if err := os.MkdirAll(filepath.Join(dir, "benchmarks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "benchmarks", vouchFileName), []byte("# reviewed 2026-09-06\n\n"+culpritEntry+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fileFP := capture()
	if fileFP.DynamicStateVouches != culprit {
		t.Fatalf("the store's vouch file did not discharge: %q, want %q", fileFP.DynamicStateVouches, culprit)
	}

	// The reviewed set has one home, the store's root: a vouches file
	// at the MODULE root — the engine's own repository file — is
	// declined by pew's engines, so it discharges nothing here
	// (REQ-pew-vouch-source; gofresh's WithoutRepositoryVouches).
	if err := os.Remove(filepath.Join(dir, "benchmarks", vouchFileName)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "vouches"), []byte(culpritEntry+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	moduleFileFP := capture()
	if moduleFileFP.DynamicStateVouches != "" {
		t.Fatalf("the module root's vouches file discharged %q; pew's engines judge under the store's set alone", moduleFileFP.DynamicStateVouches)
	}
}

// Each invocation parses its own flag set and refuses malformed entries;
// a later empty set neither inherits nor changes the earlier set.
func TestResolveVouchesSeam(t *testing.T) {
	s, err := newVouchSource("", []string{"a.example/dep:Var"})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.flags) != 1 || s.flags[0] != "a.example/dep.Var" {
		t.Fatalf("resolved set = %v", s.flags)
	}
	if _, err := newVouchSource("", []string{"garbage"}); err == nil {
		t.Fatal("malformed vouch resolved silently")
	}
	empty, err := newVouchSource("", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(empty.flags) != 0 || len(s.flags) != 1 {
		t.Fatalf("invocations share flags: old=%v new=%v", s.flags, empty.flags)
	}
}

// The vouch file's grammar: one IMPORT-PATH:VARIABLE per line, comments
// and blank lines ignored, a malformed line refusing exactly as a
// malformed flag; an absent file is the empty set; the flags extend the
// file's set and never remove from it; the set is read from the store
// --bench-dir names (REQ-pew-vouch-source).
func TestVouchFileIsTheStandingSetTheFlagsExtend(t *testing.T) {
	module := t.TempDir()
	store := filepath.Join(module, "benchmarks")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	s, _ := newVouchSource("", nil)
	if got, err := s.engineVouches(module); err != nil || len(got) != 0 {
		t.Fatalf("absent file = %v, %v; want the empty set", got, err)
	}
	if err := os.WriteFile(filepath.Join(store, vouchFileName), []byte("# standing\n\nexample.com/dep:Var\n  example.com/other:Second  \n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := s.engineVouches(module); err != nil || len(got) != 0 {
		t.Fatalf("same invocation reread standing set: %v %v", got, err)
	}
	s, _ = newVouchSource("", []string{"example.com/flag:Extra", "example.com/dep:Var"})
	got, err := s.engineVouches(module)
	if err != nil || strings.Join(got, ",") != "example.com/dep.Var,example.com/flag.Extra,example.com/other.Second" {
		t.Fatalf("file ∪ flags = %v, %v", got, err)
	}
	// --bench-dir names the store whose file governs.
	elsewhere := t.TempDir()
	if err := os.WriteFile(filepath.Join(elsewhere, vouchFileName), []byte("example.com/named:Store\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, _ = newVouchSource(elsewhere, nil)
	if got, err := s.engineVouches(module); err != nil || strings.Join(got, ",") != "example.com/named.Store" {
		t.Fatalf("named store's file = %v, %v", got, err)
	}
	// A malformed line refuses, naming the file.
	if err := os.WriteFile(filepath.Join(elsewhere, vouchFileName), []byte("garbage\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	s, _ = newVouchSource(elsewhere, nil)
	if _, err := s.engineVouches(module); err == nil || !strings.Contains(err.Error(), "vouch file") || !strings.Contains(err.Error(), "IMPORT-PATH:VARIABLE") {
		t.Fatalf("malformed vouch file = %v; want the refusal naming the file", err)
	}
}

// Every judged verb hands its --bench-dir to the vouch source before its
// first engine builds, so the store the flag names is the store whose
// vouch file governs (REQ-pew-vouch-source).
func TestJudgedVerbsNameTheirStoreForTheVouchFile(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "go.mod"), "module example.com/vouchcmd\n\ngo 1.26\n")
	writeFile(t, filepath.Join(dir, "bench_test.go"), "package vouchcmd\nimport \"testing\"\nfunc BenchmarkX(b *testing.B) {}\n")
	withWorkingDir(t, dir)
	storeDir := t.TempDir()
	writeFile(t, filepath.Join(storeDir, vouchFileName), "malformed\n")
	for _, verb := range []struct {
		name string
		cmd  func() *cobra.Command
		args []string
	}{
		{"run", newRunCmd, []string{"--bench-dir", storeDir, "."}},
		{"status", newStatusCmd, []string{"--bench-dir", storeDir, "."}},
		{"stat", newStatCmd, []string{"--bench-dir", storeDir}},
	} {
		cmd := verb.cmd()
		cmd.SetArgs(verb.args)
		cmd.SetOut(&bytes.Buffer{})
		cmd.SetErr(&bytes.Buffer{})
		if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), filepath.Join(storeDir, vouchFileName)) {
			t.Fatalf("%s ignored named store: %v", verb.name, err)
		}
	}
}
