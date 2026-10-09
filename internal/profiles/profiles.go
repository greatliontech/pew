// Package profiles admits diagnostic evidence independently of statistical rows.
// Native pprof bytes own sample definitions; summaries are disposable projections.
package profiles

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/pprof/profile"
	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/pew/internal/run"
	"golang.org/x/perf/benchfmt"
)

// Protocol identifies the execution model, not native outcome support.
const Protocol = "go-test-binary-profile/1"

// UnsupportedReason explains why a healthy profiler process proves no outcomes.
const UnsupportedReason = "profile instrumentation starts before testlog and flushes afterwards; this execution model has no admitted outcome support"

// Scope is the profiling window's meaning, never a per-operation measurement.
const Scope = "process profiling window including harness and setup; children aggregated"

// Index is the optional in-band companion index. Absence is not an empty capture.
type Index struct {
	Version  int       `json:"version"`
	Captures []Capture `json:"captures"`
}

// Child is an actual harness row and its iteration count in the diagnostic process.
type Child struct {
	Name       string `json:"name"`
	Iterations int    `json:"iterations"`
}

// Source contains exact producing bytes from a known mutable Go build input.
// Filename is evidence only: readers never open it. Package may be unknown.
type Source struct {
	Filename       string `json:"filename"`
	ModuleRelative string `json:"moduleRelative,omitempty"`
	Package        string `json:"package,omitempty"`
	Bytes          []byte `json:"bytes"`
}

// Capture records one process's evidence; its fingerprint is never borrowed from
// the statistical measurement. Revision pins the measurement rows before attachment.
type Capture struct {
	Kind                string              `json:"kind"`
	Package             string              `json:"package"`
	Benchmark           string              `json:"benchmark"`
	Selection           string              `json:"selection"`
	Children            []Child             `json:"children"`
	Budget              string              `json:"budget"`
	Protocol            string              `json:"protocol"`
	Scope               string              `json:"scope"`
	Sampling            string              `json:"sampling"`
	Commit              string              `json:"commit"`
	Dirty               bool                `json:"dirty"`
	Conditions          string              `json:"conditions"`
	BinarySHA256        string              `json:"binarySHA256"`
	MeasurementRevision string              `json:"measurementRevision"`
	Fingerprint         gofresh.Fingerprint `json:"fingerprint"`
	Ledger              string              `json:"ledger"`
	SHA256              string              `json:"sha256"`
	Size                int64               `json:"size"`
	Sources             []Source            `json:"sources,omitempty"`
}

// Digest is the lowercase content-address spelling used for immutable objects.
func Digest(b []byte) string { return fmt.Sprintf("%x", sha256.Sum256(b)) }

func digest(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// Kinds admits the public request, with stable execution order.
func Kinds(s string) ([]string, error) {
	switch s {
	case "":
		return nil, nil
	case "cpu":
		return []string{"cpu"}, nil
	case "alloc":
		return []string{"alloc"}, nil
	case "cpu,alloc":
		return []string{"cpu", "alloc"}, nil
	default:
		return nil, fmt.Errorf("profile: want cpu, alloc or cpu,alloc")
	}
}

// ValidateBudget accepts positive Go benchmark time or iteration budgets.
func ValidateBudget(s string) error {
	if v, ok := strings.CutSuffix(s, "x"); ok {
		n, err := strconv.ParseInt(v, 10, 64)
		if err == nil && n > 0 && strconv.FormatInt(n, 10) == v {
			return nil
		}
	} else if d, err := time.ParseDuration(s); err == nil && d > 0 {
		return nil
	}
	return fmt.Errorf("profile: invalid positive budget %q", s)
}

// Encode emits canonical JSON wrapped as a benchmark-format token.
func Encode(i Index) (string, error) {
	if err := i.Validate(); err != nil {
		return "", err
	}
	b, err := json.Marshal(i)
	return base64.RawURLEncoding.EncodeToString(b), err
}

// Decode refuses noncanonical, duplicate, unknown and absent evidence fields.
func Decode(s string) (Index, error) {
	b, err := base64.RawURLEncoding.Strict().DecodeString(s)
	if err != nil || base64.RawURLEncoding.EncodeToString(b) != s {
		return Index{}, fmt.Errorf("profile: invalid index encoding")
	}
	var i Index
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(&i); err != nil {
		return Index{}, err
	}
	canonical, err := Encode(i)
	if err != nil {
		return Index{}, err
	}
	if canonical != s {
		return Index{}, fmt.Errorf("profile: noncanonical index")
	}
	return i, nil
}

// FromRows checks the companion envelope separately from measurement admission.
func FromRows(rows []*benchfmt.Result) (Index, bool, error) {
	if len(rows) == 0 {
		return Index{}, false, nil
	}
	value := rows[0].GetConfig(run.KeyProfiles.Name)
	_, present := rows[0].ConfigIndex(run.KeyProfiles.Name)
	for _, r := range rows {
		_, p := r.ConfigIndex(run.KeyProfiles.Name)
		if p != present || r.GetConfig(run.KeyProfiles.Name) != value {
			return Index{}, true, fmt.Errorf("profile: index differs across measurement rows")
		}
	}
	if !present {
		return Index{Version: 1}, false, nil
	}
	i, err := Decode(value)
	return i, true, err
}

// Validate enforces the companion grammar without judging freshness or outcomes.
func (i Index) Validate() error {
	if i.Version != 1 || len(i.Captures) == 0 || len(i.Captures) > 2 {
		return fmt.Errorf("profile: invalid index version or capture set")
	}
	seen := map[string]bool{}
	for _, c := range i.Captures {
		k, err := Kinds(c.Kind)
		if err != nil || len(k) != 1 || seen[c.Kind] {
			return fmt.Errorf("profile: invalid or repeated capture kind")
		}
		seen[c.Kind] = true
		if c.Package == "" || !strings.HasPrefix(c.Benchmark, "Benchmark") || c.Selection == "" || len(c.Children) == 0 || c.Commit == "" || c.Conditions == "" || c.Size <= 0 || !digest(c.SHA256) || !digest(c.BinarySHA256) || !digest(c.MeasurementRevision) {
			return fmt.Errorf("profile: incomplete capture identity")
		}
		if c.Protocol != Protocol || c.Scope != Scope || c.Sampling != "runtime-default" {
			return fmt.Errorf("profile: unsupported capture protocol")
		}
		if err := ValidateBudget(c.Budget); err != nil {
			return err
		}
		children := map[string]bool{}
		for _, ch := range c.Children {
			if ch.Iterations <= 0 || run.BenchName(ch.Name) != c.Benchmark || children[ch.Name] {
				return fmt.Errorf("profile: invalid diagnostic child")
			}
			children[ch.Name] = true
		}
		encoded, err := run.EncodeFingerprint(c.Fingerprint)
		if err != nil {
			return err
		}
		if _, err := run.RecordedFingerprint([]benchfmt.Config{run.KeyFingerprint.Config(encoded)}); err != nil {
			return err
		}
		if _, err := run.DecodeLedger(c.Ledger); err != nil {
			return err
		}
		files := map[string]bool{}
		for _, s := range c.Sources {
			if s.Filename == "" || len(s.Bytes) == 0 || files[s.Filename] {
				return fmt.Errorf("profile: invalid source inventory")
			}
			files[s.Filename] = true
		}
	}
	return nil
}

// Revision hashes the statistical recording with the independent profile index removed.
// It is distinct from the exact byte CAS used for publication.
func Revision(rows []*benchfmt.Result) string {
	var b bytes.Buffer
	w := benchfmt.NewWriter(&b)
	for _, r := range rows {
		copy := r.Clone()
		copy.Config = nil
		for _, cfg := range r.Config {
			if cfg.Key != run.KeyProfiles.Name {
				copy.Config = append(copy.Config, cfg)
			}
		}
		_ = w.Write(copy)
	}
	return Digest(b.Bytes())
}

// ErrRelationMismatch identifies a disagreement between the saved executions,
// independently of whether either execution is fresh for today's tree.
var ErrRelationMismatch = errors.New("profile relation mismatch")

// ErrRelationUnproven identifies unavailable applicability evidence, not a pair
// disagreement. Other relation errors are operational failures. All three refuse
// attachment; readers can report an unproven relation without failing status.
var ErrRelationUnproven = errors.New("profile relation unproven")

// Relation refuses known disagreement without mistaking matching hashes for
// complete runtime outcomes. A persisted applicability endpoint alone is not
// proof; callers needing that endpoint use CheckedRelation. This profiler
// protocol always has an unverified runtime-outcome relation.
func Relation(c Capture, rows []*benchfmt.Result, fp gofresh.Fingerprint) error {
	if err := pairRelation(c, rows, fp); err != nil {
		return err
	}
	if fp.InertTestVariantApplicability != (gofresh.InertTestVariantApplicability{}) {
		return fmt.Errorf("%w: measurement applicability requires a validated engine judgment", ErrRelationUnproven)
	}
	return relation(c, rows, fp, fp.TestVariantClosure)
}

// CheckedRelation uses an applicability endpoint only after the shared engine
// proves the exact recorded measurement valid and closes its checking view.
// Neither the measurement's producing evidence nor the diagnostic is rewritten.
func CheckedRelation(ctx context.Context, c Capture, rows []*benchfmt.Result, fp gofresh.Fingerprint, parent *gofresh.View) error {
	if err := pairRelation(c, rows, fp); err != nil {
		return err
	}
	if fp.InertTestVariantApplicability == (gofresh.InertTestVariantApplicability{}) || parent == nil {
		return Relation(c, rows, fp)
	}
	subject := gofresh.Subject{Package: c.Package, Symbol: c.Benchmark}
	check, err := parent.Sibling([]gofresh.Subject{subject})
	if err != nil {
		return err
	}
	verdicts, err := check.CheckObservedBatch(ctx, map[gofresh.Subject]gofresh.Fingerprint{subject: fp})
	if err != nil {
		return applicabilityFailure(err)
	}
	if err := check.Validate(ctx); err != nil {
		return applicabilityFailure(err)
	}
	v := verdicts[subject]
	if v.Status != gofresh.Valid {
		return fmt.Errorf("%w: measurement applicability not proven: %s (%s)", ErrRelationUnproven, v.Status, v.Reason)
	}
	return relation(c, rows, fp, fp.EffectiveTestVariantClosure())
}

func relation(c Capture, rows []*benchfmt.Result, fp gofresh.Fingerprint, measurementVariant string) error {
	if err := pairRelation(c, rows, fp); err != nil {
		return err
	}
	if c.Fingerprint.TestVariantClosure != measurementVariant {
		return fmt.Errorf("%w: test variants differ", ErrRelationMismatch)
	}
	return nil
}

func applicabilityFailure(err error) error {
	if errors.Is(err, gofresh.ErrAnalysisUnavailable) {
		return fmt.Errorf("%w: %w", ErrRelationUnproven, err)
	}
	return err
}

// These contradictions need no current applicability proof. Test variants are
// checked separately, against the producing variant or a proved endpoint.
func pairRelation(c Capture, rows []*benchfmt.Result, fp gofresh.Fingerprint) error {
	if len(rows) == 0 || c.MeasurementRevision != Revision(rows) {
		return fmt.Errorf("%w: measurement revision differs", ErrRelationMismatch)
	}
	if c.Fingerprint.MaximalClosure != fp.MaximalClosure || c.Fingerprint.Guards != fp.Guards {
		return fmt.Errorf("%w: source or build/runtime guards differ", ErrRelationMismatch)
	}
	children := map[string]bool{}
	for _, r := range rows {
		children[string(r.Name)] = true
	}
	if len(children) != len(c.Children) {
		return fmt.Errorf("%w: diagnostic workload differs", ErrRelationMismatch)
	}
	for _, ch := range c.Children {
		if !children[ch.Name] {
			return fmt.Errorf("%w: diagnostic workload differs", ErrRelationMismatch)
		}
	}
	// Different manifests can include different observed subsets, not just changed
	// values. Their shared identities are compared by the caller's native checks.
	return nil
}

// Weight names a derived symbol/source attribution. Flat weights are disjoint;
// cumulative weights count each symbol once per sample and overlap across symbols.
type Weight struct {
	Symbol      string `json:"symbol"`
	Source      string `json:"source,omitempty"`
	Attribution string `json:"attribution"`
	Flat        int64  `json:"flat"`
	Cumulative  int64  `json:"cumulative"`
}

// Metric is one native sample kind; values are not per-operation costs.
type Metric struct {
	Type    string   `json:"type"`
	Unit    string   `json:"unit"`
	Total   int64    `json:"total"`
	Weights []Weight `json:"weights"`
}

// Analyze validates native bytes and accounts for every sample without opening
// profile filenames. Empty selected kinds are reported by the caller explicitly.
func Analyze(b []byte, kind string, sources []Source) ([]Metric, error) {
	p, err := profile.ParseData(b)
	if err != nil {
		return nil, fmt.Errorf("profile: parse: %w", err)
	}
	if err := p.CheckValid(); err != nil {
		return nil, fmt.Errorf("profile: invalid: %w", err)
	}
	var out []Metric
	// Every physical location/inline frame is resolved once per analysis. Heap
	// profiles can repeat the same stack in thousands of sampled allocations.
	frames := make(map[*profile.Sample][]Weight, len(p.Sample))
	resolved := map[profile.Line]Weight{}
	for _, sample := range p.Sample {
		frames[sample] = sampleFrames(sample, sources, resolved)
	}
	seenTypes := map[string]bool{}
	for index, typ := range p.SampleType {
		key := typ.Type + "/" + typ.Unit
		if seenTypes[key] {
			return nil, fmt.Errorf("profile: duplicate sample type")
		}
		seenTypes[key] = true
		wanted := kind == "cpu" && typ.Type == "cpu" && typ.Unit == "nanoseconds" || kind == "alloc" && (typ.Type == "alloc_space" && typ.Unit == "bytes" || typ.Type == "alloc_objects" && typ.Unit == "count")
		if !wanted {
			continue
		}
		m := Metric{Type: typ.Type, Unit: typ.Unit}
		weights := map[string]*Weight{}
		for _, sample := range p.Sample {
			v := sample.Value[index]
			if v < 0 || m.Total > math.MaxInt64-v {
				return nil, fmt.Errorf("profile: negative or overflowing sample weights")
			}
			m.Total += v
			seen := map[string]bool{}
			for j, frame := range frames[sample] {
				key := frame.Symbol + "\x00" + frame.Source + "\x00" + frame.Attribution
				if weights[key] == nil {
					copy := frame
					weights[key] = &copy
				}
				w := weights[key]
				if j == 0 {
					w.Flat += v
				}
				if !seen[key] {
					w.Cumulative += v
					seen[key] = true
				}
			}
		}
		for _, w := range weights {
			m.Weights = append(m.Weights, *w)
		}
		sort.Slice(m.Weights, func(i, j int) bool {
			a, b := m.Weights[i], m.Weights[j]
			if a.Flat != b.Flat {
				return a.Flat > b.Flat
			}
			if a.Symbol != b.Symbol {
				return a.Symbol < b.Symbol
			}
			return a.Source < b.Source
		})
		out = append(out, m)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("profile: requested %s sample kind absent", kind)
	}
	return out, nil
}

// Empty reports absence of sampled weight, not absence of execution or work.
func Empty(m []Metric) bool {
	for _, v := range m {
		if v.Total != 0 {
			return false
		}
	}
	return true
}
