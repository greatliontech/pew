package profiles

import (
	"fmt"
	"sort"

	"github.com/google/pprof/profile"
	"github.com/greatliontech/pew/internal/run"
)

// Difference separates raw process-window weights from normalized flat shares.
// Neither quantity estimates per-operation cost or a statistically causal effect.
type Difference struct {
	Type      string               `json:"type"`
	Unit      string               `json:"unit"`
	BaseTotal int64                `json:"baseTotal"`
	NewTotal  int64                `json:"newTotal"`
	Functions []FunctionDifference `json:"functions"`
}

// FunctionDifference retains functions present on only one side. A nil share
// means the side's total is zero, not a zero share or evidence of absent work.
type FunctionDifference struct {
	Symbol         string   `json:"symbol"`
	BaseFlat       int64    `json:"baseFlat"`
	NewFlat        int64    `json:"newFlat"`
	BaseCumulative int64    `json:"baseCumulative"`
	NewCumulative  int64    `json:"newCumulative"`
	BaseShare      *float64 `json:"baseShare"`
	NewShare       *float64 `json:"newShare"`
}

// Compare admits cross-execution comparability, not current-tree reuse. Source
// closures, producing paths and executable digests deliberately need not agree.
// Each side's exact-source attribution remains in its separately rendered metrics.
func Compare(a, b Capture, rawA, rawB []byte) ([]Difference, error) {
	if a.Kind != b.Kind || a.Package != b.Package || a.Benchmark != b.Benchmark || a.Selection != b.Selection || a.Budget != b.Budget || a.Protocol != b.Protocol || a.Scope != b.Scope || a.Sampling != b.Sampling {
		return nil, fmt.Errorf("profile: incompatible workload or capture protocol")
	}
	ga, gb := run.GuardConfig(a.Fingerprint.Guards), run.GuardConfig(b.Fingerprint.Guards)
	for i := range ga {
		if len(ga[i].Value) == 0 || string(ga[i].Value) != string(gb[i].Value) {
			return nil, fmt.Errorf("profile: incompatible %s", ga[i].Key)
		}
	}
	children := map[string]bool{}
	for _, ch := range a.Children {
		children[ch.Name] = true
	}
	if len(children) != len(b.Children) {
		return nil, fmt.Errorf("profile: incompatible children")
	}
	for _, ch := range b.Children {
		if !children[ch.Name] {
			return nil, fmt.Errorf("profile: incompatible children")
		}
	}
	pa, err := profile.ParseData(rawA)
	if err != nil {
		return nil, err
	}
	pb, err := profile.ParseData(rawB)
	if err != nil {
		return nil, err
	}
	if pa.Period != pb.Period || !samePeriodType(pa.PeriodType, pb.PeriodType) {
		return nil, fmt.Errorf("profile: incompatible native sampling period")
	}
	ma, err := Analyze(rawA, a.Kind, nil)
	if err != nil {
		return nil, err
	}
	mb, err := Analyze(rawB, b.Kind, nil)
	if err != nil {
		return nil, err
	}
	byType := map[string]Metric{}
	for _, m := range mb {
		byType[m.Type+"\x00"+m.Unit] = m
	}
	if len(ma) != len(mb) {
		return nil, fmt.Errorf("profile: incompatible sample types")
	}
	var out []Difference
	for _, m := range ma {
		n, ok := byType[m.Type+"\x00"+m.Unit]
		if !ok {
			return nil, fmt.Errorf("profile: incompatible sample type %s/%s", m.Type, m.Unit)
		}
		d := Difference{Type: m.Type, Unit: m.Unit, BaseTotal: m.Total, NewTotal: n.Total}
		functions := map[string]*FunctionDifference{}
		for side, metric := range []Metric{m, n} {
			for _, weight := range metric.Weights {
				f := functions[weight.Symbol]
				if f == nil {
					f = &FunctionDifference{Symbol: weight.Symbol}
					functions[weight.Symbol] = f
				}
				if side == 0 {
					f.BaseFlat += weight.Flat
					f.BaseCumulative += weight.Cumulative
				} else {
					f.NewFlat += weight.Flat
					f.NewCumulative += weight.Cumulative
				}
			}
		}
		for _, f := range functions {
			if m.Total != 0 {
				s := float64(f.BaseFlat) / float64(m.Total)
				f.BaseShare = &s
			}
			if n.Total != 0 {
				s := float64(f.NewFlat) / float64(n.Total)
				f.NewShare = &s
			}
			d.Functions = append(d.Functions, *f)
		}
		sort.Slice(d.Functions, func(i, j int) bool { return d.Functions[i].Symbol < d.Functions[j].Symbol })
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Type+"/"+out[i].Unit < out[j].Type+"/"+out[j].Unit })
	return out, nil
}

func samePeriodType(a, b *profile.ValueType) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
