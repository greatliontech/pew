// Package run drives `go test -bench`, captures the benchmark-format output, and
// demuxes it per top-level benchmark for storage with provenance (spec §9).
package run

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/greatliontech/gofresh/guard"
	"golang.org/x/perf/benchfmt"
)

// Options are the run-hygiene knobs (spec §9), all with configurable defaults.
type Options struct {
	Count     int    // -count (default 10)
	Benchtime string // -benchtime (default 1s)
	Bench     string // -bench pattern (default ".")
}

// RecordingFormat is the current in-band Pew recording format.
const RecordingFormat = "1"

// TestArgs builds the `go test` argument list for benchmarking pkg.
func TestArgs(pkg string, o Options) []string {
	return []string{
		"test", "-run", "^$", "-bench", o.Bench, "-benchmem",
		"-count", strconv.Itoa(o.Count), "-benchtime", o.Benchtime, pkg,
	}
}

// Execute runs the benchmark command (optionally pinned via `taskset -c <pin>`)
// in dir and returns stdout (the benchmark-format output).
func Execute(dir, pin string, env, args []string) ([]byte, error) {
	name, full := "go", args
	if pin != "" {
		name, full = "taskset", append([]string{"-c", pin, "go"}, args...)
	}
	cmd := exec.Command(name, full...)
	cmd.Dir = dir
	commandEnv, err := commandEnvironment(env, dir)
	if err != nil {
		return nil, fmt.Errorf("run: command environment: %w", err)
	}
	cmd.Env = commandEnv
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("run: %s %s: %w: %s",
			name, strings.Join(full, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func commandEnvironment(env []string, dir string) ([]string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	command := make([]string, 0, len(env)+1)
	for _, entry := range env {
		name, _, ok := strings.Cut(entry, "=")
		if ok && equalEnvKey(name, "PWD") {
			continue
		}
		command = append(command, entry)
	}
	return append(command, "PWD="+abs), nil
}

func equalEnvKey(left, right string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

// A CorruptLine is one line of the transient `go test` stream that carried
// benchmark-result bytes unusable as data. The stream is not a recording: a
// benchmark or its dependencies may write to stdout at any point, including
// between the framework's un-newlined benchmark-name print and its result
// fields, splicing foreign bytes into a result line (spec §9). A splice leaves
// two shapes: the result line with foreign text where its fields belong (a
// parser syntax error), and the detached measurement tail on its own line
// (Orphan). Corrupt lines are never recorded and never silently dropped.
type CorruptLine struct {
	Line   int    // 1-based line number in the go test stream
	Text   string // the offending line, verbatim
	Cause  string // parser message, or the orphaned-tail description
	Bench  string // attributed top-level benchmark ("" when the line names none)
	Orphan bool   // a detached measurement tail: a sample was destroyed or replaced
}

// Parse reads benchmark-format output into results, collecting rather than
// failing on lines corrupted by interleaved foreign output (the parser treats
// syntax errors as per-record, by design). Reserved provenance keys in the
// output remain a hard error (spec §5: refused before storage).
func Parse(out []byte) ([]*benchfmt.Result, []CorruptLine, error) {
	lines := bytes.Split(out, []byte{'\n'})
	for _, line := range lines {
		colon := bytes.IndexByte(line, ':')
		if colon < 0 {
			continue
		}
		key := string(line[:colon])
		if strings.HasPrefix(key, "pew-") || reservedConfigKey(key) {
			return nil, nil, fmt.Errorf("run: benchmark output uses reserved %s configuration", key)
		}
	}
	r := benchfmt.NewReader(bytes.NewReader(out), "go test")
	var results []*benchfmt.Result
	var corrupt []CorruptLine
	for r.Scan() {
		switch rec := r.Result().(type) {
		case *benchfmt.Result:
			results = append(results, rec.Clone())
		case *benchfmt.SyntaxError:
			corrupt = append(corrupt, corruptAt(lines, rec.Line, rec.Msg))
		}
	}
	if err := r.Err(); err != nil {
		return nil, nil, fmt.Errorf("run: read benchmark output: %w", err)
	}
	corrupt = append(corrupt, orphanedTails(lines)...)
	sort.SliceStable(corrupt, func(i, j int) bool { return corrupt[i].Line < corrupt[j].Line })
	return results, corrupt, nil
}

func corruptAt(lines [][]byte, n int, cause string) CorruptLine {
	cl := CorruptLine{Line: n, Cause: cause}
	if n >= 1 && n <= len(lines) {
		cl.Text = string(lines[n-1])
		cl.Bench = lineBench(lines[n-1])
	}
	return cl
}

// lineBench derives the top-level benchmark a stream line claims to belong to
// from its leading "Benchmark..." field, or "" when it carries none.
func lineBench(line []byte) string {
	if !bytes.HasPrefix(line, benchmarkLinePrefix) {
		return ""
	}
	field := line
	if i := bytes.IndexAny(line, " \t"); i >= 0 {
		field = line[:i]
	}
	name := string(field[len(benchmarkLinePrefix):])
	if name == "" {
		return ""
	}
	return BenchName(name)
}

var benchmarkLinePrefix = []byte("Benchmark")

// orphanedTails flags lines shaped like the measurement fields of a result
// line without its "Benchmark..." name — the detached tail a splice leaves
// behind. The parser silently ignores such a line, so without this check the
// destroyed (or, worse, replaced) sample would vanish without a trace. Each
// tail is attributed to the nearest preceding line that names a benchmark:
// the framework prints one result line at a time, so only foreign output can
// stand between a corrupted result line and its detached tail.
func orphanedTails(lines [][]byte) []CorruptLine {
	var out []CorruptLine
	bench := ""
	for i, line := range lines {
		if bytes.HasPrefix(line, benchmarkLinePrefix) {
			bench = lineBench(line)
			continue
		}
		if !measurementTail(line) {
			continue
		}
		out = append(out, CorruptLine{
			Line:   i + 1,
			Text:   string(line),
			Cause:  "orphaned measurement fields (a result line was split by foreign output)",
			Bench:  bench,
			Orphan: true,
		})
	}
	return out
}

// measurementTail reports whether line has the exact field shape of a result
// line's tail: a positive iteration count, then value/unit pairs of which at
// least one is a per-iteration metric. No other line the `go test` stream
// produces has this shape (config keys start with a letter, PASS/ok/Unit
// lines with a word).
func measurementTail(line []byte) bool {
	fields := strings.Fields(string(line))
	if len(fields) < 3 || len(fields)%2 == 0 {
		return false
	}
	iters, err := strconv.ParseInt(fields[0], 10, 64)
	if err != nil || iters <= 0 {
		return false
	}
	perIter := false
	for i := 1; i < len(fields); i += 2 {
		if _, err := strconv.ParseFloat(fields[i], 64); err != nil {
			return false
		}
		unit := fields[i+1]
		if unit == "" || (unit[0] >= '0' && unit[0] <= '9') {
			return false
		}
		if strings.HasSuffix(unit, "/op") {
			perIter = true
		}
	}
	return perIter
}

// A StreamAudit is the per-benchmark disposition of a parsed `go test` stream
// against the demanded sample count (spec §9 sample floor).
type StreamAudit struct {
	// Refused maps each top-level benchmark that must not be recorded to its
	// reasons. A refused benchmark's completed prior recording is left
	// untouched; the package's other benchmarks record normally.
	Refused map[string][]string
	// PackageCause, when non-empty, names corruption that cannot be localized
	// to one selected benchmark — a sample was destroyed or replaced somewhere
	// unattributable — so the whole package's recording must be refused.
	PackageCause string
}

// AuditStream applies the spec §9 sample floor to a parsed stream: every
// result row of a recordable benchmark carries exactly count samples, and no
// corruption evidence is attributed to it. selected is the set of top-level
// benchmarks this run may record; corruption attributed elsewhere cannot
// refuse a recording, but an orphaned tail attributed elsewhere (or nowhere)
// taints the whole package.
func AuditStream(results []*benchfmt.Result, corrupt []CorruptLine, count int, selected []string) StreamAudit {
	sel := make(map[string]bool, len(selected))
	for _, s := range selected {
		sel[s] = true
	}
	audit := StreamAudit{Refused: map[string][]string{}}
	for _, cl := range corrupt {
		switch {
		case sel[cl.Bench]:
			audit.Refused[cl.Bench] = append(audit.Refused[cl.Bench],
				fmt.Sprintf("line %d: %q (%s)", cl.Line, cl.Text, cl.Cause))
		case cl.Orphan:
			if audit.PackageCause == "" {
				audit.PackageCause = fmt.Sprintf("line %d: %q (%s; not attributable to a benchmark of this run)",
					cl.Line, cl.Text, cl.Cause)
			}
		}
	}
	rows := map[string]int{}
	var order []string
	for _, r := range results {
		name := string(r.Name)
		if rows[name] == 0 {
			order = append(order, name)
		}
		rows[name]++
	}
	for _, name := range order {
		if n := rows[name]; n != count {
			bench := BenchName(name)
			if sel[bench] {
				audit.Refused[bench] = append(audit.Refused[bench],
					fmt.Sprintf("result row %s has %d of %d samples", name, n, count))
			}
		}
	}
	return audit
}

func reservedConfigKey(key string) bool {
	switch key {
	case "commit", "toolchain", "machine", "buildconfig", "runtimeconfig", "dirty", "pure":
		return true
	default:
		return false
	}
}

// BenchName derives the storage function name ("BenchmarkXxx") from a benchfmt
// result name ("Xxx-8", "Xxx/sub-8"): benchfmt strips the "Benchmark" prefix, the
// framework appends a "-<gomaxprocs>" suffix, and sub-benchmarks add "/...". The
// storage unit is the top-level function.
func BenchName(resultName string) string {
	base := resultName
	if i := strings.IndexByte(base, '/'); i >= 0 {
		base = base[:i] // drop sub-benchmark path; the file is the top-level function
	}
	if i := strings.LastIndexByte(base, '-'); i >= 0 {
		base = base[:i] // drop the "-<gomaxprocs>" suffix (the only '-' in a func name)
	}
	return "Benchmark" + base
}

// ProvenanceConfig returns the in-band provenance lines in spec §5 order: the
// measured commit and dirty flag from pew's git layer, the gofresh guard
// values, and the observed run conditions (§9 — provenance only, never a guard,
// INV-9). File:true so benchfmt.Writer emits them as `key: value` lines (it omits
// File==false config as internal).
func ProvenanceConfig(commit string, dirty bool, g guard.Guards, conditions Conditions) []benchfmt.Config {
	return []benchfmt.Config{
		{Key: "pew-format", Value: []byte(RecordingFormat), File: true},
		{Key: "commit", Value: []byte(commit), File: true},
		{Key: "toolchain", Value: []byte(g.Toolchain), File: true},
		{Key: "machine", Value: []byte(g.Machine), File: true},
		{Key: "buildconfig", Value: []byte(g.BuildConfig), File: true},
		{Key: "runtimeconfig", Value: []byte(g.RuntimeConfig), File: true},
		{Key: "dirty", Value: []byte(strconv.FormatBool(dirty)), File: true},
		{Key: "pew-runconditions", Value: []byte(conditions.String()), File: true},
	}
}

// ClosureConfig is the recorded closure-hash line. File:true so benchfmt.Writer
// emits it (it omits File==false config as internal).
func ClosureConfig(hash string) benchfmt.Config {
	return benchfmt.Config{Key: "pew-closure", Value: []byte(hash), File: true}
}

// GofreshPurityConfig records the attributable purity evidence used by capture.
func GofreshPurityConfig(attribution string) benchfmt.Config {
	return benchfmt.Config{Key: "pew-purity", Value: []byte(attribution), File: true}
}

// PureConfig is the recorded purity flag ("true" for --assume-pure, "false" for
// --impure). File:true so it serializes.
func PureConfig(v string) benchfmt.Config {
	return benchfmt.Config{Key: "pure", Value: []byte(v), File: true}
}

// RuntimeConfig records the runtime-input guard and its manifest (§7.8).
func RuntimeConfig(digest, manifest string) []benchfmt.Config {
	return []benchfmt.Config{
		{Key: "pew-runtime", Value: []byte(digest), File: true},
		{Key: "pew-runtime-inputs", Value: []byte(manifest), File: true},
	}
}

// Demux groups results by top-level benchmark function, appending extra config
// (provenance + guard configs) to each. Results are cloned defensively.
func Demux(results []*benchfmt.Result, extra []benchfmt.Config) map[string][]*benchfmt.Result {
	groups := map[string][]*benchfmt.Result{}
	for _, r := range results {
		rc := r.Clone()
		rc.Config = append(append([]benchfmt.Config{}, rc.Config...), extra...)
		name := BenchName(string(r.Name))
		groups[name] = append(groups[name], rc)
	}
	return groups
}
