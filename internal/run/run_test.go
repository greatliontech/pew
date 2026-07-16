package run

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh/guard"
	"golang.org/x/perf/benchfmt"
)

// TestProvenanceConfigKeysAndOrder pins the in-band provenance lines (spec §5):
// keys, order, and serializability.
func TestProvenanceConfigKeysAndOrder(t *testing.T) {
	cfgs := ProvenanceConfig("c1", true, guard.Guards{
		Toolchain: "tc", BuildConfig: "bc", Machine: "m", RuntimeConfig: "rc",
	})
	want := []struct{ key, value string }{
		{"pew-format", RecordingFormat},
		{"commit", "c1"},
		{"toolchain", "tc"},
		{"machine", "m"},
		{"buildconfig", "bc"},
		{"runtimeconfig", "rc"},
		{"dirty", "true"},
	}
	if len(cfgs) != len(want) {
		t.Fatalf("got %d config lines, want %d", len(cfgs), len(want))
	}
	for i, w := range want {
		if cfgs[i].Key != w.key || string(cfgs[i].Value) != w.value {
			t.Errorf("config[%d] = %s: %s, want %s: %s", i, cfgs[i].Key, cfgs[i].Value, w.key, w.value)
		}
		if !cfgs[i].File {
			t.Errorf("%s config must have File:true", cfgs[i].Key)
		}
	}
}

func TestBenchName(t *testing.T) {
	for in, want := range map[string]string{
		"HashFiles-8":      "BenchmarkHashFiles",
		"Run-16":           "BenchmarkRun",
		"Run/case=big-8":   "BenchmarkRun",
		"Marshal/n=10-4":   "BenchmarkMarshal",
		"Parse/a-b/c=1-32": "BenchmarkParse",
		"NoSuffix":         "BenchmarkNoSuffix",
	} {
		if got := BenchName(in); got != want {
			t.Errorf("BenchName(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestRecordedConfigSerializable pins File:true on the run-constructed config —
// without it benchfmt.Writer silently omits the line and every recording reads
// stale (the bug this guards).
func TestRecordedConfigSerializable(t *testing.T) {
	if !ClosureConfig("x").File {
		t.Error("pew-closure config must have File:true")
	}
	if !PureConfig("true").File {
		t.Error("pure config must have File:true")
	}
	if cfg := GofreshPurityConfig("source directive"); !cfg.File || cfg.Key != "pew-purity" || string(cfg.Value) != "source directive" {
		t.Errorf("gofresh purity config = %+v", cfg)
	}
	for _, cfg := range RuntimeConfig("rt1", "manifest1") {
		if !cfg.File {
			t.Errorf("%s config must have File:true", cfg.Key)
		}
	}
}

func TestTestArgs(t *testing.T) {
	tests := []struct {
		name string
		opts Options
		want []string
	}{
		{
			name: "defaults",
			opts: Options{Count: 10, Benchtime: "1s", Bench: "."},
			want: []string{"test", "-run", "^$", "-bench", ".", "-benchmem", "-count", "10", "-benchtime", "1s", "example/p"},
		},
		{
			name: "overrides",
			opts: Options{Count: 3, Benchtime: "250ms", Bench: "BenchmarkHash"},
			want: []string{"test", "-run", "^$", "-bench", "BenchmarkHash", "-benchmem", "-count", "3", "-benchtime", "250ms", "example/p"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := TestArgs("example/p", tt.opts)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("TestArgs = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExecuteDerivesCommandEnvironment(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/commandenv\n\ngo 1.26.4\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nimport (\"fmt\"; \"os\")\n\nfunc main() { fmt.Println(os.Getenv(\"PWD\")); fmt.Println(os.Getenv(\"GOWORK\")) }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var env []string
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GOWORK=") && !strings.HasPrefix(entry, "PWD=") {
			env = append(env, entry)
		}
	}
	env = append(env, "GOWORK=off", "PWD=/wrong")
	out, err := Execute(dir, "", env, []string{"run", "."})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n"); !reflect.DeepEqual(got, []string{dir, "off"}) {
		t.Fatalf("go env PWD GOWORK = %q, want [%s off]", got, dir)
	}
}

const benchOut = `goos: linux
goarch: amd64
pkg: example/p
cpu: TestCPU
BenchmarkRun-8 1000000 1234 ns/op 456 B/op 7 allocs/op
BenchmarkRun/sub-8 500000 2000 ns/op 8 B/op 1 allocs/op
BenchmarkOther-8 200000 6000 ns/op 0 B/op 0 allocs/op
PASS
ok  	example/p	1.234s
`

func TestParseAndDemux(t *testing.T) {
	results, err := Parse([]byte(benchOut))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("got %d results, want 3 (PASS/ok lines must be ignored)", len(results))
	}

	extra := []benchfmt.Config{{Key: "pew-closure", Value: []byte("cl1")}}
	groups := Demux(results, extra)

	var names []string
	for n := range groups {
		names = append(names, n)
	}
	sort.Strings(names)
	if !reflect.DeepEqual(names, []string{"BenchmarkOther", "BenchmarkRun"}) {
		t.Errorf("groups = %v, want [BenchmarkOther BenchmarkRun]", names)
	}
	if len(groups["BenchmarkRun"]) != 2 { // Run-8 + Run/sub-8 share the function file
		t.Errorf("BenchmarkRun rows = %d, want 2", len(groups["BenchmarkRun"]))
	}
	// extra config injected, original config preserved.
	cfg := groups["BenchmarkRun"][0].Config
	if cfg[len(cfg)-1].Key != "pew-closure" || string(cfg[len(cfg)-1].Value) != "cl1" {
		t.Errorf("pew-closure not appended: %v", cfg)
	}
	if cfg[0].Key != "goos" {
		t.Errorf("original config lost; first key = %q", cfg[0].Key)
	}
}

func TestParseRejectsReservedFormatConfig(t *testing.T) {
	for name, line := range map[string]string{
		"format-space":  "pew-format: 2",
		"format-tab":    "pew-format:\t2",
		"format-delete": "pew-format:",
		"purity":        "pure: true",
		"guard":         "commit: forged",
		"unknown-pew":   "pew-future: forged",
	} {
		t.Run(name, func(t *testing.T) {
			out := []byte(line + "\nBenchmarkRun-8 1 1 ns/op\n")
			if _, err := Parse(out); err == nil {
				t.Fatalf("reserved configuration %q accepted", line)
			}
		})
	}
}
