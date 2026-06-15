package run

import (
	"reflect"
	"sort"
	"testing"

	"golang.org/x/perf/benchfmt"
)

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
}

func TestTestArgs(t *testing.T) {
	got := TestArgs("example/p", Options{Count: 10, Benchtime: "1s", Bench: "."})
	want := []string{"test", "-run", "^$", "-bench", ".", "-benchmem", "-count", "10", "-benchtime", "1s", "example/p"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("TestArgs = %v, want %v", got, want)
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
