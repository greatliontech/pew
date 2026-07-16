package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/guard"
	"github.com/greatliontech/gofresh/runtimeinput"
	runpkg "github.com/greatliontech/pew/internal/run"
	"github.com/greatliontech/pew/internal/store"
	"golang.org/x/perf/benchfmt"
)

// TestApplyPurity pins the recorded purity-flag semantics (spec §7.3, §7.5):
// --impure re-runs on any non-stale verdict; --assume-pure lifts unverifiable to
// valid after every hashable guard held; a stale verdict is never overridden.
func TestApplyPurity(t *testing.T) {
	staleV := gofresh.Verdict{Status: gofresh.Stale, Reason: "closure"}
	validV := gofresh.Verdict{Status: gofresh.Valid}
	unverV := gofresh.Verdict{Status: gofresh.Unverifiable, Reason: "reaches os.Open"}

	tests := []struct {
		name string
		in   gofresh.Verdict
		pure string
		want gofresh.Verdict
	}{
		{"no flag passes through valid", validV, "", validV},
		{"no flag passes through unverifiable", unverV, "", unverV},
		{"impure demotes valid", validV, "false", gofresh.Verdict{Status: gofresh.Unverifiable, Reason: "impure"}},
		{"impure demotes unverifiable", unverV, "false", gofresh.Verdict{Status: gofresh.Unverifiable, Reason: "impure"}},
		{"impure never overrides stale", staleV, "false", staleV},
		{"assume-pure lifts unverifiable", unverV, "true", validV},
		{"assume-pure never lifts stale", staleV, "true", staleV},
		{"assume-pure leaves valid alone", validV, "true", validV},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := applyPurity(tc.in, tc.pure); got != tc.want {
				t.Errorf("applyPurity(%+v, %q) = %+v, want %+v", tc.in, tc.pure, got, tc.want)
			}
		})
	}
}

// TestFingerprintFromConfig pins the config-line ↔ fingerprint mapping (spec §5:
// pew owns the serialization; gofresh owns the semantics).
func TestFingerprintFromConfig(t *testing.T) {
	cfg := []benchfmt.Config{
		{Key: "commit", Value: []byte("c1")},
		{Key: "toolchain", Value: []byte("tc")},
		{Key: "machine", Value: []byte("m")},
		{Key: "buildconfig", Value: []byte("bc")},
		{Key: "runtimeconfig", Value: []byte("rc")},
		{Key: "pew-closure", Value: []byte("cl")},
		{Key: "pew-runtime", Value: []byte("rd")},
		{Key: "pew-runtime-inputs", Value: []byte("manifest")},
		{Key: "pew-purity", Value: []byte("source directive")},
		{Key: "pure", Value: []byte("true")},
	}
	fp, pure := fingerprintFromConfig(cfg)
	if fp.MaximalClosure != "cl" || fp.RuntimeInputs != "manifest" || fp.RuntimeDigest != "rd" || fp.PurityAssertion != "source directive" || fp.ResultKind != gofresh.Measurement {
		t.Errorf("fingerprint closure/runtime fields = %+v", fp)
	}
	g := fp.Guards
	if g.Toolchain != "tc" || g.BuildConfig != "bc" || g.Machine != "m" || g.RuntimeConfig != "rc" {
		t.Errorf("guards = %+v", g)
	}
	if pure != "true" {
		t.Errorf("pure = %q, want true", pure)
	}

	fp, pure = fingerprintFromConfig(nil)
	if fp.ResultKind != gofresh.Measurement || fp.MaximalClosure != "" || fp.Guards != (guard.Guards{}) || pure != "" {
		t.Errorf("empty config: fp=%+v pure=%q, want empty measurement", fp, pure)
	}
}

func TestLegacyRuntimeManifestsUseOrdinaryChecking(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/legacyruntime\n\ngo 1.26.4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bench_test.go"), []byte("package legacyruntime\n\nimport \"testing\"\n\nfunc BenchmarkNoIO(b *testing.B) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(dir, "fixture.txt")
	if err := os.WriteFile(fixture, []byte("original\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	e, err := newEngine(dir)
	if err != nil {
		t.Fatal(err)
	}
	const pkg = "example.com/legacyruntime"
	const bench = "BenchmarkNoIO"
	subject := gofresh.Subject{Package: pkg, Symbol: bench}
	fp, err := e.CaptureFor(subject, dir, gofresh.Measurement)
	if err != nil {
		t.Fatal(err)
	}
	complete, err := runtimeinput.FromTestLog([]byte("open fixture.txt\n"), dir, dir, runtimeinput.WithCompletedProcess("legacy-package-test-binary"))
	if err != nil {
		t.Fatal(err)
	}
	st := store.New(t.TempDir())
	write := func(label string, state runtimeinput.State) {
		t.Helper()
		cfg := append(runpkg.ProvenanceConfig("c1", false, fp.Guards), runpkg.ClosureConfig(fp.MaximalClosure))
		cfg = append(cfg, runpkg.RuntimeConfig(state.Digest, state.Manifest)...)
		recs := []*benchfmt.Result{{Name: benchfmt.Name("NoIO"), Iters: 1, Values: []benchfmt.Value{{Value: 1, Unit: "sec/op"}}, Config: cfg}}
		if err := st.Write("", bench, label, recs); err != nil {
			t.Fatal(err)
		}
	}
	write("complete", complete.State)

	v, reason, err := checkOne(st, e, pkg, "", dir, bench, "complete")
	if err != nil {
		t.Fatal(err)
	}
	if v != verdictValid || reason != "" {
		t.Fatalf("unchanged complete legacy manifest = {%s %q}, want valid", v, reason)
	}
	if err := os.WriteFile(fixture, []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	v, reason, err = checkOne(st, e, pkg, "", dir, bench, "complete")
	if err != nil {
		t.Fatal(err)
	}
	if v != verdictStale || reason != "runtimeinputs" {
		t.Fatalf("changed complete legacy manifest = {%s %q}, want stale runtimeinputs", v, reason)
	}

	if err := os.WriteFile(fixture, []byte("original\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	unverifiable, err := runtimeinput.FromTestLog([]byte("stat fixture.txt\n"), dir, dir, runtimeinput.WithCompletedProcess("legacy-package-test-binary"))
	if err != nil {
		t.Fatal(err)
	}
	write("unverifiable", unverifiable.State)
	v, reason, err = checkOne(st, e, pkg, "", dir, bench, "unverifiable")
	if err != nil {
		t.Fatal(err)
	}
	if v != verdictUnverifiable || !strings.Contains(reason, "stat metadata input") {
		t.Fatalf("unverifiable legacy manifest = {%s %q}, want unverifiable metadata reason", v, reason)
	}
}
