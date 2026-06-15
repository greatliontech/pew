package main

import (
	"testing"

	"github.com/thegrumpylion/pew/internal/compare"
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
