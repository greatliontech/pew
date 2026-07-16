package main

import (
	"testing"

	gofresh "github.com/greatliontech/gofresh"
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
		{Key: "pew-format", Value: []byte(runpkg.RecordingFormat)},
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
	fp, pure, ok := fingerprintFromConfig(cfg)
	if !ok {
		t.Fatal("current recording format rejected")
	}
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
	unknown := append([]benchfmt.Config(nil), cfg...)
	unknown[0].Value = []byte("2")
	duplicate := append(append([]benchfmt.Config(nil), cfg...), benchfmt.Config{Key: "pew-format", Value: []byte(runpkg.RecordingFormat)})
	for name, malformed := range map[string][]benchfmt.Config{"unknown": unknown, "duplicate": duplicate} {
		if _, _, ok := fingerprintFromConfig(malformed); ok {
			t.Errorf("%s recording format accepted", name)
		}
	}

	fp, pure, ok = fingerprintFromConfig(nil)
	if ok || fp != (gofresh.Fingerprint{}) || pure != "" {
		t.Errorf("unversioned config: fp=%+v pure=%q ok=%v, want rejection", fp, pure, ok)
	}
}

func TestUnversionedRecordingIsStale(t *testing.T) {
	st := store.New(t.TempDir())
	recs := []*benchfmt.Result{{Name: benchfmt.Name("NoIO"), Iters: 1, Values: []benchfmt.Value{{Value: 1, Unit: "sec/op"}}, Config: []benchfmt.Config{{Key: "pew-runtime", Value: []byte("old")}}}}
	if err := st.Write("", "BenchmarkNoIO", "", recs); err != nil {
		t.Fatal(err)
	}
	v, reason, err := checkOne(st, nil, "example.com/old", "", "", "BenchmarkNoIO", "")
	if err != nil {
		t.Fatal(err)
	}
	if v != verdictStale || reason != "format" {
		t.Fatalf("unversioned recording = {%s %q}, want stale format", v, reason)
	}
	incomplete := []*benchfmt.Result{{Name: benchfmt.Name("NoIO"), Iters: 1, Values: []benchfmt.Value{{Value: 1, Unit: "sec/op"}}, Config: []benchfmt.Config{
		{Key: "pew-format", Value: []byte(runpkg.RecordingFormat), File: true},
		{Key: "toolchain", Value: []byte("go"), File: true}, {Key: "machine", Value: []byte("m"), File: true},
		{Key: "buildconfig", Value: []byte("b"), File: true}, {Key: "runtimeconfig", Value: []byte("r"), File: true},
		{Key: "dirty", Value: []byte("false"), File: true}, {Key: "pew-closure", Value: []byte("c"), File: true},
		{Key: "pew-runtime", Value: []byte("d"), File: true}, {Key: "pew-runtime-inputs", Value: []byte("i"), File: true},
	}}}
	if err := st.Write("", "BenchmarkNoIO", "incomplete", incomplete); err != nil {
		t.Fatal(err)
	}
	v, reason, err = checkOne(st, nil, "example.com/old", "", "", "BenchmarkNoIO", "incomplete")
	if err != nil {
		t.Fatal(err)
	}
	if v != verdictStale || reason != "format" {
		t.Fatalf("incomplete format-1 recording = {%s %q}, want stale format", v, reason)
	}
}
