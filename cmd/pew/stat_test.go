package main

import (
	"testing"

	"github.com/thegrumpylion/pew/internal/closure"
	"github.com/thegrumpylion/pew/internal/compare"
	"github.com/thegrumpylion/pew/internal/provenance"
	"github.com/thegrumpylion/pew/internal/store"
	"golang.org/x/perf/benchfmt"
)

func TestBaselineFor(t *testing.T) {
	tests := []struct {
		refs              []string
		wantBase, wantNew string
		wantErr           bool
	}{
		{nil, "HEAD", "", false},                // auto: working tree vs HEAD
		{[]string{"v1"}, "v1", "", false},       // pinned: working tree vs v1
		{[]string{"a", "b"}, "a", "b", false},   // A/B: a vs b
		{[]string{"a", "b", "c"}, "", "", true}, // too many
	}
	for _, tt := range tests {
		bl, err := baselineFor(tt.refs)
		if (err != nil) != tt.wantErr {
			t.Errorf("baselineFor(%v) err=%v, wantErr=%v", tt.refs, err, tt.wantErr)
			continue
		}
		if err != nil {
			continue
		}
		if bl.baseRef != tt.wantBase || bl.newRef != tt.wantNew {
			t.Errorf("baselineFor(%v) = {%q,%q}, want {%q,%q}", tt.refs, bl.baseRef, bl.newRef, tt.wantBase, tt.wantNew)
		}
	}
}

func TestParseGateUnits(t *testing.T) {
	ok := []struct {
		in   string
		want []string
	}{
		{"sec/op", []string{"sec/op"}},
		{"sec/op,B/op", []string{"sec/op", "B/op"}},
		{"sec/op B/op allocs/op", []string{"sec/op", "B/op", "allocs/op"}},
		{"sec/op, allocs/op", []string{"sec/op", "allocs/op"}},
	}
	for _, tt := range ok {
		got, err := parseGateUnits(tt.in)
		if err != nil {
			t.Errorf("parseGateUnits(%q): %v", tt.in, err)
			continue
		}
		for _, u := range tt.want {
			if !got[u] {
				t.Errorf("parseGateUnits(%q) missing %q", tt.in, u)
			}
		}
		if len(got) != len(tt.want) {
			t.Errorf("parseGateUnits(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}

	for _, bad := range []string{"", "  ", "ns/op", "sec/op,bogus", "cpu"} {
		if _, err := parseGateUnits(bad); err == nil {
			t.Errorf("parseGateUnits(%q): want error", bad)
		}
	}
}

func TestValidateOptions(t *testing.T) {
	base := compare.DefaultOptions()
	if err := validateOptions(base); err != nil {
		t.Errorf("defaults rejected: %v", err)
	}
	zeroThresh := base
	zeroThresh.ThresholdPct = 0 // legitimate: "any significant worse change"
	if err := validateOptions(zeroThresh); err != nil {
		t.Errorf("threshold 0 rejected: %v", err)
	}

	bad := []compare.Options{
		{Alpha: 0, ThresholdPct: 3, Confidence: 0.95},     // alpha too low (would silently pass everything)
		{Alpha: 1, ThresholdPct: 3, Confidence: 0.95},     // alpha too high
		{Alpha: 0.05, ThresholdPct: -1, Confidence: 0.95}, // negative floor guts the magnitude gate
		{Alpha: 0.05, ThresholdPct: 3, Confidence: 0},     // confidence out of range
		{Alpha: 0.05, ThresholdPct: 3, Confidence: 1},
	}
	for _, o := range bad {
		if err := validateOptions(o); err == nil {
			t.Errorf("validateOptions(%+v): want error", o)
		}
	}
}

func TestIsDirty(t *testing.T) {
	dirty := []*benchfmt.Result{{Config: []benchfmt.Config{{Key: "dirty", Value: []byte("true"), File: true}}}}
	clean := []*benchfmt.Result{{Config: []benchfmt.Config{{Key: "dirty", Value: []byte("false"), File: true}}}}
	none := []*benchfmt.Result{{}}
	if !isDirty(dirty) {
		t.Error("isDirty(dirty)=false")
	}
	if isDirty(clean) {
		t.Error("isDirty(clean)=true")
	}
	if isDirty(none) {
		t.Error("isDirty(no-flag)=true")
	}
	if isDirty(nil) {
		t.Error("isDirty(nil)=true")
	}
}

func TestNonValidUsesLabel(t *testing.T) {
	h, err := closure.New()
	if err != nil {
		t.Fatalf("New closure hasher: %v", err)
	}
	const pkg = "github.com/thegrumpylion/pew/internal/closure/fixtures/initregistry/bench"
	const bench = "BenchmarkDecode"
	cl, err := h.Compute(pkg, bench)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	p := provenance.Provenance{Commit: "c1", Toolchain: "go1.26.4", Machine: "m1", BuildConfig: "b1"}
	st := store.New(t.TempDir())
	write := func(label, hash string) {
		t.Helper()
		cfg := []benchfmt.Config{
			{Key: "commit", Value: []byte(p.Commit), File: true},
			{Key: "toolchain", Value: []byte(p.Toolchain), File: true},
			{Key: "machine", Value: []byte(p.Machine), File: true},
			{Key: "buildconfig", Value: []byte(p.BuildConfig), File: true},
			{Key: "pew-closure", Value: []byte(hash), File: true},
			{Key: "dirty", Value: []byte("false"), File: true},
		}
		recs := []*benchfmt.Result{{Name: benchfmt.Name(bench), Iters: 1, Values: []benchfmt.Value{{Value: 1, Unit: "sec/op"}}, Config: cfg}}
		if err := st.Write("", bench, label, recs); err != nil {
			t.Fatalf("Write(%q): %v", label, err)
		}
	}
	write("", cl.Hash)
	write("x", cl.Hash+"-stale")

	need, err := nonValid(st, h, pkg, "", "x", []string{bench}, p)
	if err != nil {
		t.Fatalf("nonValid labeled: %v", err)
	}
	if len(need) != 1 || need[0] != bench {
		t.Fatalf("labeled nonValid = %v, want [%s]", need, bench)
	}
	need, err = nonValid(st, h, pkg, "", "", []string{bench}, p)
	if err != nil {
		t.Fatalf("nonValid unlabeled: %v", err)
	}
	if len(need) != 0 {
		t.Fatalf("unlabeled nonValid = %v, want none", need)
	}
}
