// Package run drives `go test -bench`, captures the benchmark-format output, and
// demuxes it per top-level benchmark for storage with provenance (spec §9).
package run

import (
	"bytes"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
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

// Parse reads benchmark-format output into results.
func Parse(out []byte) ([]*benchfmt.Result, error) {
	r := benchfmt.NewReader(bytes.NewReader(out), "go test")
	var results []*benchfmt.Result
	for r.Scan() {
		switch rec := r.Result().(type) {
		case *benchfmt.Result:
			results = append(results, rec.Clone())
		case *benchfmt.SyntaxError:
			return nil, fmt.Errorf("run: parse benchmark output: %w", rec)
		}
	}
	if err := r.Err(); err != nil {
		return nil, fmt.Errorf("run: read benchmark output: %w", err)
	}
	return results, nil
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
// measured commit and dirty flag from pew's git layer, and the gofresh guard
// values. File:true so benchfmt.Writer emits them as `key: value` lines (it omits
// File==false config as internal).
func ProvenanceConfig(commit string, dirty bool, g guard.Guards) []benchfmt.Config {
	return []benchfmt.Config{
		{Key: "commit", Value: []byte(commit), File: true},
		{Key: "toolchain", Value: []byte(g.Toolchain), File: true},
		{Key: "machine", Value: []byte(g.Machine), File: true},
		{Key: "buildconfig", Value: []byte(g.BuildConfig), File: true},
		{Key: "runtimeconfig", Value: []byte(g.RuntimeConfig), File: true},
		{Key: "dirty", Value: []byte(strconv.FormatBool(dirty)), File: true},
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
