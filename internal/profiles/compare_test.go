package profiles

import (
	"bytes"
	"math"
	"reflect"
	"testing"

	"github.com/google/pprof/profile"
)

func distribution(t testing.TB, alloc, reorder bool, symbol string, values []byte) []byte {
	t.Helper()
	f := &profile.Function{ID: 1, Name: symbol}
	l := &profile.Location{ID: 1, Line: []profile.Line{{Function: f}}}
	p := &profile.Profile{Function: []*profile.Function{f}, Location: []*profile.Location{l}, Period: 100, PeriodType: &profile.ValueType{Type: "cpu", Unit: "nanoseconds"}, SampleType: []*profile.ValueType{{Type: "cpu", Unit: "nanoseconds"}}}
	if alloc {
		p.SampleType = []*profile.ValueType{{Type: "alloc_objects", Unit: "count"}, {Type: "alloc_space", Unit: "bytes"}}
		p.PeriodType = &profile.ValueType{Type: "space", Unit: "bytes"}
	}
	if alloc && reorder {
		p.SampleType[0], p.SampleType[1] = p.SampleType[1], p.SampleType[0]
	}
	for i, value := range values {
		s := &profile.Sample{Value: []int64{int64(value)}}
		if alloc {
			s.Value = []int64{int64(value), int64(value) * 128}
			if reorder {
				s.Value[0], s.Value[1] = s.Value[1], s.Value[0]
			}
		}
		if i%2 == 0 {
			s.Location = []*profile.Location{l, l}
		}
		p.Sample = append(p.Sample, s)
	}
	var b bytes.Buffer
	if err := p.Write(&b); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func FuzzProfileDifferences(f *testing.F) {
	f.Add(false, []byte{1, 2, 3}, []byte{4, 5, 0})
	f.Add(true, []byte{1, 200, 3}, []byte{0, 0})
	f.Add(true, []byte{}, []byte{255})
	f.Fuzz(func(t *testing.T, alloc bool, left, right []byte) {
		a, b := testIndex(t).Captures[0], testIndex(t).Captures[0]
		if alloc {
			a.Kind, b.Kind = "alloc", "alloc"
		}
		b.Fingerprint.MaximalClosure += "changed-source"
		b.Fingerprint.TestVariantClosure += "changed-tests"
		b.BinarySHA256 = Digest([]byte("different binary"))
		b.Children = []Child{{Name: a.Children[0].Name, Iterations: 999}}
		x := distribution(t, alloc, false, "left-only", left)
		y := distribution(t, alloc, true, "right-only", right)
		diffs, err := Compare(a, b, x, y)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range diffs {
			var wantA, wantB int64
			for _, v := range left {
				wantA += int64(v)
			}
			for _, v := range right {
				wantB += int64(v)
			}
			if d.Type == "alloc_space" {
				wantA *= 128
				wantB *= 128
			}
			if d.BaseTotal != wantA || d.NewTotal != wantB {
				t.Fatalf("sample type/order confusion: %+v want %d/%d", d, wantA, wantB)
			}
			var flatA, flatB int64
			var shareA, shareB float64
			for _, fn := range d.Functions {
				flatA += fn.BaseFlat
				flatB += fn.NewFlat
				if (fn.BaseShare == nil) != (wantA == 0) || (fn.NewShare == nil) != (wantB == 0) {
					t.Fatal("zero share defined")
				}
				if fn.BaseShare != nil {
					shareA += *fn.BaseShare
				}
				if fn.NewShare != nil {
					shareB += *fn.NewShare
				}
				if fn.BaseCumulative > d.BaseTotal || fn.NewCumulative > d.NewTotal {
					t.Fatal("recursive stack double counted")
				}
			}
			if flatA != wantA || flatB != wantB {
				t.Fatal("unmatched/unknown weights lost")
			}
			if wantA > 0 && math.Abs(shareA-1) > 1e-12 || wantB > 0 && math.Abs(shareB-1) > 1e-12 {
				t.Fatal("shares not conserved")
			}
		}
		ordered, err := Compare(a, b, x, distribution(t, alloc, false, "right-only", right))
		if err != nil || !reflect.DeepEqual(diffs, ordered) {
			t.Fatal("table order changed comparison", err)
		}
	})
}

func TestProfileComparisonCompatibility(t *testing.T) {
	a := testIndex(t).Captures[0]
	raw := distribution(t, false, false, "work", []byte{1, 2})
	for _, change := range []func(*Capture){
		func(c *Capture) { c.Kind = "alloc" }, func(c *Capture) { c.Package += "other" }, func(c *Capture) { c.Benchmark += "other" }, func(c *Capture) { c.Selection += "other" }, func(c *Capture) { c.Budget = "2s" }, func(c *Capture) { c.Protocol += "other" }, func(c *Capture) { c.Scope += "other" }, func(c *Capture) { c.Sampling += "other" }, func(c *Capture) { c.Children = nil }, func(c *Capture) { c.Children = []Child{{Name: "other", Iterations: 1}} }, func(c *Capture) { c.Fingerprint.Guards.Toolchain = "" }, func(c *Capture) { c.Fingerprint.Guards.Machine += "other" }, func(c *Capture) { c.Fingerprint.Guards.BuildConfig += "other" }, func(c *Capture) { c.Fingerprint.Guards.RuntimeConfig += "other" },
	} {
		b := a
		change(&b)
		if _, err := Compare(a, b, raw, raw); err == nil {
			t.Fatalf("incompatible capture accepted: %+v", b)
		}
	}
	for _, change := range []func(*profile.Profile){func(p *profile.Profile) { p.Period++ }, func(p *profile.Profile) { p.PeriodType.Unit = "other" }, func(p *profile.Profile) { p.SampleType[0].Unit = "other" }} {
		p, err := profile.ParseData(raw)
		if err != nil {
			t.Fatal(err)
		}
		change(p)
		var b bytes.Buffer
		if err := p.Write(&b); err != nil {
			t.Fatal(err)
		}
		if _, err := Compare(a, a, raw, b.Bytes()); err == nil {
			t.Fatal("native incompatibility accepted")
		}
	}
	if _, err := Compare(a, a, raw, []byte("corrupt")); err == nil {
		t.Fatal("corrupt profile compared")
	}
}
