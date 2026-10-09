package compare

import (
	"fmt"
	"sort"
	"strings"
)

// Policies govern gate eligibility, never whether admitted numbers are shown.
// The zero value preserves the ordinary partial, informational policy.
type Policies struct {
	Coverage   string `json:"coverage"`
	Freshness  string `json:"freshness"`
	Conditions string `json:"conditions"`
}

// Effective supplies the documented defaults.
func (p Policies) Effective() Policies {
	if p.Coverage == "" {
		p.Coverage = "partial"
	}
	if p.Freshness == "" {
		p.Freshness = "report"
	}
	if p.Conditions == "" {
		p.Conditions = "report"
	}
	return p
}

// Validate refuses unknown policy values before inventory or analysis.
func (p Policies) Validate() error {
	p = p.Effective()
	for _, v := range []struct{ name, value, a, b string }{
		{"coverage", p.Coverage, "partial", "complete"},
		{"freshness", p.Freshness, "report", "require"},
		{"conditions", p.Conditions, "report", "compatible"},
	} {
		if v.value != v.a && v.value != v.b {
			return fmt.Errorf("stat: --%s must be %s or %s, got %q", v.name, v.a, v.b, v.value)
		}
	}
	return nil
}

// Cause records a reason without exposing internal guard digests.
type Cause struct {
	Code    string `json:"code"`
	Side    string `json:"side,omitempty"`
	Message string `json:"message"`
}

// Subject identifies the inventoried recording independently of optional stream
// configuration. Two storage subjects must never merge through an omitted pkg.
type Subject struct {
	// Location is the canonical recording coordinate, independent of ref-local
	// package names. It is an internal grouping key, never rendered.
	Location  string
	Package   string
	Recording string
	Label     string
}

// Disposition is the single accounting record for a result/unit obligation, or
// for a recording whose child result identities could not be established.
type Disposition struct {
	BasePackage string  `json:"basePackage,omitempty"`
	NewPackage  string  `json:"newPackage,omitempty"`
	Package     string  `json:"package,omitempty"`
	Label       string  `json:"label,omitempty"`
	Recording   string  `json:"recording,omitempty"`
	Benchmark   string  `json:"benchmark,omitempty"`
	Config      string  `json:"config,omitempty"`
	Unit        string  `json:"unit,omitempty"`
	Requested   bool    `json:"requested"`
	Compared    bool    `json:"compared"`
	Eligible    bool    `json:"eligible"`
	Regression  bool    `json:"regression"`
	Freshness   string  `json:"freshness,omitempty"`
	Conditions  string  `json:"conditions,omitempty"`
	Causes      []Cause `json:"causes,omitempty"`
}

func causeRank(code string) int {
	switch code {
	case "inventory":
		return 0
	case "format":
		return 1
	case "strategy":
		return 2
	case "dirty-ref":
		return 3
	case "missing-side":
		return 4
	case "guard":
		return 5
	case "missing-unit":
		return 6
	case "invalid-sample", "invalid-statistic":
		return 7
	case "freshness":
		return 8
	case "conditions":
		return 9
	}
	return 10
}

// OrderCauses gives all causes a deterministic primary-cause precedence.
func (d *Disposition) OrderCauses() {
	sort.SliceStable(d.Causes, func(i, j int) bool {
		a, b := d.Causes[i], d.Causes[j]
		if causeRank(a.Code) != causeRank(b.Code) {
			return causeRank(a.Code) < causeRank(b.Code)
		}
		return false // retain the registry's guard order and base-before-new order
	})
}

// Text is the human projection of precisely the same disposition JSON carries.
func (d Disposition) Text() string {
	name := d.Benchmark
	if name == "" {
		name = d.Recording
	}
	if pkg := packageLabel(d.Package, d.BasePackage, d.NewPackage); pkg != "" {
		name = pkg + "." + name
	}
	if d.Label != "" {
		name += " label=" + d.Label
	}
	if d.Config != "" {
		name += " [" + d.Config + "]"
	}
	if d.Unit != "" {
		name += " " + d.Unit
	}
	state := "not compared"
	if d.Compared {
		state = "compared"
	}
	state += fmt.Sprintf("; requested=%t eligible=%t regression=%t", d.Requested, d.Eligible, d.Regression)
	if d.Freshness != "" {
		state += "; freshness=" + d.Freshness
	}
	if d.Conditions != "" {
		state += "; conditions=" + d.Conditions
	}
	for _, c := range d.Causes {
		state += "; " + c.Code
		if c.Side != "" {
			state += " (" + c.Side + ")"
		}
		state += ": " + c.Message
	}
	return name + ": " + state
}

func commonPackage(base, newer string) string {
	if base == "" {
		return newer
	}
	if newer == "" || base == newer {
		return base
	}
	return ""
}

func packageLabel(common, base, newer string) string {
	if common != "" {
		return common
	}
	if base != "" || newer != "" {
		return "[base=" + base + " new=" + newer + "]"
	}
	return ""
}

// Coverage is derived from dispositions each time it is requested.
type Coverage struct {
	Requested         int      `json:"requested"`
	Compared          int      `json:"compared"`
	Eligible          int      `json:"eligible"`
	Missing           int      `json:"missing"`
	Regressed         int      `json:"regressed"`
	InventoryComplete bool     `json:"inventoryComplete"`
	Satisfied         bool     `json:"satisfied"`
	Policies          Policies `json:"policies"`
}

// Coverage returns the gate's accounting over requested obligations only.
func (r *Result) Coverage(p Policies) Coverage {
	c := Coverage{InventoryComplete: true, Policies: p.Effective()}
	for _, d := range r.Dispositions {
		for _, cause := range d.Causes {
			if cause.Code == "inventory" {
				c.InventoryComplete = false
			}
		}
		if !d.Requested {
			continue
		}
		c.Requested++
		if d.Compared {
			c.Compared++
		}
		if d.Compared && d.Eligible {
			c.Eligible++
			if d.Regression {
				c.Regressed++
			}
		}
	}
	c.Missing = c.Requested - c.Eligible
	c.Satisfied = c.Eligible > 0 && (c.Policies.Coverage == "partial" || (c.Missing == 0 && c.InventoryComplete))
	return c
}

// Text states the counts and policies without conflating comparison with admission.
func (c Coverage) Text() string {
	return fmt.Sprintf("coverage: requested=%d compared=%d eligible=%d missing=%d regressed=%d inventoryComplete=%t satisfied=%t (coverage=%s freshness=%s conditions=%s)", c.Requested, c.Compared, c.Eligible, c.Missing, c.Regressed, c.InventoryComplete, c.Satisfied, c.Policies.Coverage, c.Policies.Freshness, c.Policies.Conditions)
}

// EmptyReason explains a gate that has no eligible evidence, retaining every cause.
func (r *Result) EmptyReason(p Policies) string {
	p = p.Effective()
	if len(r.Dispositions) == 0 {
		return "no recordings on either side (run `pew run` first)"
	}
	counts := map[string]int{}
	seen := map[string]bool{}
	for _, d := range r.Dispositions {
		if !d.Requested || (d.Compared && d.Eligible) {
			continue
		}
		for _, c := range d.Causes {
			if (c.Code == "conditions" && p.Conditions == "report") || (c.Code == "freshness" && p.Freshness == "report") {
				continue
			}
			key := strings.Join([]string{d.Package, d.Label, d.Recording, d.Benchmark, d.Config, d.Unit, c.Code, c.Side, c.Message}, "\x00")
			if seen[key] {
				continue
			}
			seen[key] = true
			name := c.Code
			if name == "format" {
				name = "stale format"
			}
			if name == "strategy" {
				name = "stale dynamic-state strategy"
			}
			if name == "dirty-ref" {
				name = "dirty recording"
			}
			counts[name]++
		}
	}
	var parts []string
	for name, n := range counts {
		parts = append(parts, fmt.Sprintf("%s: %d", name, n))
	}
	sort.Strings(parts)
	if len(parts) == 0 {
		return "metrics compared, none on an eligible gated unit"
	}
	return "comparison obligations incomplete (" + strings.Join(parts, "; ") + ")"
}

// deriveLegacyCounts projects benchmark-level display counters from obligations.
// They are never a second gate authority.
func (r *Result) deriveLegacyCounts() {
	type state struct{ missingSide, guard, common, missingUnit bool }
	groups := map[string]*state{}
	for _, d := range r.Dispositions {
		key := strings.Join([]string{d.Package, d.Recording, d.Label, d.Config, d.Benchmark}, "\x00")
		s := groups[key]
		if s == nil {
			s = &state{}
			groups[key] = s
		}
		missingUnit := false
		for _, c := range d.Causes {
			switch c.Code {
			case "missing-side":
				s.missingSide = true
			case "guard":
				s.guard = true
			case "missing-unit":
				missingUnit = true
				s.missingUnit = true
			}
		}
		if !missingUnit {
			s.common = true
		}
	}
	for _, s := range groups {
		if s.missingSide {
			r.OneSided++
		} else if s.guard {
			r.GuardMismatch++
		} else if !s.common && s.missingUnit {
			r.NoCommonUnit++
		}
	}
}
