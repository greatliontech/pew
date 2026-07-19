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
	load := 0.03
	cfgs := ProvenanceConfig("c1", true, guard.Guards{
		Toolchain: "tc", BuildConfig: "bc", Machine: "m", RuntimeConfig: "rc",
	}, Conditions{Governor: "performance", Load1: &load})
	want := []struct{ key, value string }{
		{"pew-format", RecordingFormat},
		{"commit", "c1"},
		{"toolchain", "tc"},
		{"machine", "m"},
		{"buildconfig", "bc"},
		{"runtimeconfig", "rc"},
		{"dirty", "true"},
		{"pew-runconditions", "governor=performance turbo=unknown load1=0.03 throttled=unknown battery=unknown"},
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
	results, corrupt, dropped, err := Parse([]byte(benchOut))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(corrupt) != 0 {
		t.Fatalf("clean stream flagged corrupt lines: %v", corrupt)
	}
	if len(dropped) != 0 {
		t.Fatalf("toolchain-only stream dropped configuration: %v", dropped)
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

// TestPGOInput pins spec §9's PGO resolution: an explicit -pgo=<path> names a
// profile for any package; under auto/absent only a tested *main* package's
// default.pgo applies; off suppresses everything; the digest covers the
// profile's bytes; later flags win; relative paths resolve against the module
// root; a profile the compile will consume but pew cannot read fails closed —
// as does the empty `-pgo=` path, which fails every build.
func TestPGOInput(t *testing.T) {
	dir := t.TempDir()
	pkgDir := filepath.Join(dir, "cmd", "tool")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	abs := filepath.Join(dir, "p.pgo")
	if err := os.WriteFile(abs, []byte("profile-a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "default.pgo"), []byte("default-profile"), 0o644); err != nil {
		t.Fatal(err)
	}

	for name, tc := range map[string]struct {
		goflags string
		mainPkg bool
		want    string // "" = none, "digest" = some pgo: digest, "error" = fail
	}{
		"library, no flags":              {goflags: "", want: ""},
		"library, auto":                  {goflags: "-pgo=auto", want: ""},
		"main, no flags, default.pgo":    {goflags: "", mainPkg: true, want: "digest"},
		"main, auto, default.pgo":        {goflags: "-pgo=auto", mainPkg: true, want: "digest"},
		"main, off suppresses default":   {goflags: "-pgo=off", mainPkg: true, want: ""},
		"main, explicit beats default":   {goflags: "-pgo=" + abs, mainPkg: true, want: "digest"},
		"other flags only":               {goflags: "-tags=exp -race", want: ""},
		"absolute path":                  {goflags: "-pgo=" + abs, want: "digest"},
		"relative path":                  {goflags: "-pgo=p.pgo", want: "digest"},
		"last flag wins":                 {goflags: "-pgo=" + abs + " -pgo=off", want: ""},
		"unreadable profile":             {goflags: "-pgo=missing.pgo", want: "error"},
		"empty -pgo= path fails closed":  {goflags: "-pgo=", want: "error"},
		"main without default, no flags": {goflags: "", mainPkg: true, want: "none-without-default"},
	} {
		t.Run(name, func(t *testing.T) {
			pd := pkgDir
			if tc.want == "none-without-default" {
				pd = t.TempDir()
				tc.want = ""
			}
			got, err := PGOInput(dir, pd, tc.mainPkg, tc.goflags)
			switch tc.want {
			case "error":
				if err == nil {
					t.Fatalf("PGOInput = %q, want fail-closed error", got)
				}
			case "":
				if err != nil || got != "" {
					t.Fatalf("PGOInput = %q, %v, want no profile", got, err)
				}
			case "digest":
				if err != nil || !strings.HasPrefix(got, "pgo:") {
					t.Fatalf("PGOInput = %q, %v, want a pgo: digest", got, err)
				}
			}
		})
	}

	// The digest tracks content for both channels.
	explicitA, err := PGOInput(dir, pkgDir, false, "-pgo=p.pgo")
	if err != nil {
		t.Fatal(err)
	}
	defaultA, err := PGOInput(dir, pkgDir, true, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte("profile-b"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "default.pgo"), []byte("default-profile-2"), 0o644); err != nil {
		t.Fatal(err)
	}
	explicitB, err := PGOInput(dir, pkgDir, false, "-pgo=p.pgo")
	if err != nil {
		t.Fatal(err)
	}
	defaultB, err := PGOInput(dir, pkgDir, true, "")
	if err != nil {
		t.Fatal(err)
	}
	if explicitA == explicitB {
		t.Fatal("explicit profile content change did not move the digest")
	}
	if defaultA == defaultB {
		t.Fatal("default.pgo content change did not move the digest")
	}
}

// TestEffectiveGoflags pins the resolution channel (spec §9): GOFLAGS written
// to the go env file (`go env -w`) applies to builds without any process
// environment entry, so pew must read the effective value through `go env` —
// scanning the process env alone would miss a profile configured that way.
func TestEffectiveGoflags(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/goflagsprobe\n\ngo 1.26\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	goenv := filepath.Join(t.TempDir(), "goenv.cfg")
	if err := os.WriteFile(goenv, []byte("GOFLAGS=-pgo=env-file.pgo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var env []string
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GOFLAGS=") && !strings.HasPrefix(entry, "GOENV=") {
			env = append(env, entry)
		}
	}
	fromFile, err := EffectiveGoflags(dir, append(env, "GOENV="+goenv))
	if err != nil {
		t.Fatal(err)
	}
	if fromFile != "-pgo=env-file.pgo" {
		t.Fatalf("EffectiveGoflags = %q, want the env-file value", fromFile)
	}
	// The process variable wins over the file, per the go command.
	fromEnv, err := EffectiveGoflags(dir, append(env, "GOENV="+goenv, "GOFLAGS=-pgo=proc.pgo"))
	if err != nil {
		t.Fatal(err)
	}
	if fromEnv != "-pgo=proc.pgo" {
		t.Fatalf("EffectiveGoflags = %q, want the process value", fromEnv)
	}
}

// TestParseDropsForeignStreamConfig pins spec §5's closed recording key set
// (INV-12): a dependency logging a lowercase colon-terminated first word
// (`raft: appending entries`) is read by benchfmt as a file configuration
// line, and without the strip every later result — and the stored recording —
// would carry transient log text as durable configuration, fragmenting `stat`
// grouping. The foreign key must be dropped from every result and reported
// exactly once; the toolchain's own keys survive.
func TestParseDropsForeignStreamConfig(t *testing.T) {
	stream := `goos: linux
goarch: amd64
pkg: example/p
cpu: TestCPU
raft: appending entries
BenchmarkRun-8 1000000 1234 ns/op
warning: slow disk
raft: appending entries
BenchmarkOther-8 200000 6000 ns/op
PASS
ok  	example/p	1.234s
`
	results, corrupt, dropped, err := Parse([]byte(stream))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(corrupt) != 0 {
		t.Fatalf("config-shaped log lines flagged corrupt: %+v", corrupt)
	}
	wantDropped := []DroppedConfig{
		{Key: "raft", Value: "appending entries"},
		{Key: "warning", Value: "slow disk"},
	}
	if !reflect.DeepEqual(dropped, wantDropped) {
		t.Fatalf("dropped = %+v, want %+v", dropped, wantDropped)
	}
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	for _, r := range results {
		for _, c := range r.Config {
			if c.File && !toolchainConfigKey(c.Key) {
				t.Errorf("result %s still carries foreign config %s: %s", r.Name, c.Key, c.Value)
			}
		}
	}
	if _, ok := results[0].ConfigIndex("goos"); !ok {
		t.Error("toolchain goos config lost in the strip")
	}
}

// TestRecordingConfigKeySetIsClosed is INV-12's anchor: composing a parsed
// stream (foreign keys stripped) with everything the run path appends yields
// serializable (File:true) config drawn only from the closed set — the
// toolchain's four keys, pew's provenance and guard keys, and `pure`. The
// store writer emits exactly the File:true entries, so this key set is what a
// recording can ever contain.
func TestRecordingConfigKeySetIsClosed(t *testing.T) {
	stream := "goos: linux\npkg: example/p\nraft: x\nBenchmarkRun-8 100 5 ns/op\n"
	results, _, _, err := Parse([]byte(stream))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	extra := ProvenanceConfig("c1", false, guard.Guards{Toolchain: "tc", BuildConfig: "bc", Machine: "m", RuntimeConfig: "rc"}, Conditions{})
	extra = append(extra, RuntimeConfig("rt", "manifest")...)
	extra = append(extra, ClosureConfig("cl"), GofreshPurityConfig("d"), PureConfig("true"))
	closed := map[string]bool{
		"goos": true, "goarch": true, "pkg": true, "cpu": true,
		"pew-format": true, "commit": true, "toolchain": true, "machine": true,
		"buildconfig": true, "runtimeconfig": true, "dirty": true,
		"pew-runconditions": true, "pew-runtime": true, "pew-runtime-inputs": true,
		"pew-closure": true, "pew-purity": true, "pure": true,
	}
	for _, group := range Demux(results, extra) {
		for _, r := range group {
			for _, c := range r.Config {
				if c.File && !closed[c.Key] {
					t.Errorf("recording would carry key %q outside the closed set", c.Key)
				}
			}
		}
	}
}

// TestParseCollectsCorruptionFromInterleavedStream runs Parse over a stream
// assembled verbatim from a real `go test -bench` capture of a package whose
// benchmarks start a consensus node logging to stdout (protodb ./internal/db):
// the framework prints the benchmark name without a newline, the dependency's
// logger splices its line into the result line, and the measurement fields land
// orphaned on their own line. One corrupted benchmark must not poison the
// stream: the clean benchmark's samples parse, every corrupt line is collected
// with its position, verbatim text, and attribution, and the sample floor
// refuses only the corrupted benchmark.
func TestParseCollectsCorruptionFromInterleavedStream(t *testing.T) {
	out, err := os.ReadFile(filepath.Join("testdata", "interleaved-go-test-stream.txt"))
	if err != nil {
		t.Fatal(err)
	}
	results, corrupt, _, err := Parse(out)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	var names []string
	for _, r := range results {
		names = append(names, string(r.Name))
	}
	wantNames := []string{
		"KVSeamGet/stack=embedded-8",
		"KVSeamGet/stack=embedded-8",
		"KVSeamGet/stack=embedded-8",
		"KVSeamCommit/stack=raft/batch=1-8",
	}
	if !reflect.DeepEqual(names, wantNames) {
		t.Errorf("parsed rows = %v, want %v", names, wantNames)
	}

	want := []struct {
		line   int
		orphan bool
		prefix string
	}{
		{11, false, "BenchmarkKVSeamCommit/stack=raft/batch=1-8"},
		{17, true, "   22239"},
		{18, false, "BenchmarkKVSeamCommit/stack=raft/batch=1-8"},
		{25, true, "   23276"},
	}
	if len(corrupt) != len(want) {
		t.Fatalf("corrupt lines = %+v, want %d", corrupt, len(want))
	}
	for i, w := range want {
		cl := corrupt[i]
		if cl.Line != w.line || cl.Orphan != w.orphan || !strings.HasPrefix(cl.Text, w.prefix) {
			t.Errorf("corrupt[%d] = %+v, want line %d orphan %v prefix %q", i, cl, w.line, w.orphan, w.prefix)
		}
		if cl.Bench != "BenchmarkKVSeamCommit" {
			t.Errorf("corrupt[%d] attributed to %q, want BenchmarkKVSeamCommit", i, cl.Bench)
		}
		if cl.Cause == "" {
			t.Errorf("corrupt[%d] has no cause", i)
		}
	}

	audit := AuditStream(results, corrupt, 3, []string{"BenchmarkKVSeamCommit", "BenchmarkKVSeamGet"})
	if audit.PackageCause != "" {
		t.Errorf("attributable corruption raised package cause %q", audit.PackageCause)
	}
	if _, ok := audit.Refused["BenchmarkKVSeamGet"]; ok {
		t.Errorf("clean benchmark refused: %v", audit.Refused["BenchmarkKVSeamGet"])
	}
	reasons := audit.Refused["BenchmarkKVSeamCommit"]
	if len(reasons) != 5 { // 4 corrupt lines + the 1-of-3 sample deficit
		t.Fatalf("BenchmarkKVSeamCommit reasons = %q, want 5", reasons)
	}
	if got := reasons[len(reasons)-1]; !strings.Contains(got, "1 of 3 samples") {
		t.Errorf("deficit reason = %q, want 1 of 3 samples", got)
	}
}

// TestParseOrphanedTailDetection pins the measurement-tail classifier: the
// detached tail of a split result line is flagged, and every near-miss line the
// go test stream legitimately produces is not.
func TestParseOrphanedTailDetection(t *testing.T) {
	for name, tc := range map[string]struct {
		line   string
		orphan bool
	}{
		"real tail":              {"   22239\t     50758 ns/op\t   47375 B/op\t      48 allocs/op", true},
		"tail single metric":     {"1000000 1234 ns/op", true},
		"pass line":              {"PASS", false},
		"ok line":                {"ok  \texample/p\t1.234s", false},
		"config line":            {"cpu: TestCPU", false},
		"log line":               {"2026-07-18 03:26:29.246778 I | dragonboat: dragonboat version: 4.0.0 (Dev)", false},
		"zero iterations":        {"0 1234 ns/op", false},
		"negative iterations":    {"-5 1234 ns/op", false},
		"no per-op unit":         {"12 34.5 MB/s", false},
		"missing unit":           {"12 34.5", false},
		"numeric unit":           {"12 34.5 6", false},
		"non-numeric value":      {"12 fast ns/op", false},
		"unit line":              {"Unit ns/op better=lower", false},
		"benchmark line skipped": {"BenchmarkRun-8 1000 1234 ns/op", false},
	} {
		t.Run(name, func(t *testing.T) {
			out := []byte("BenchmarkAnchor-8 1 1 ns/op\n" + tc.line + "\n")
			_, corrupt, _, err := Parse(out)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			var orphans []CorruptLine
			for _, cl := range corrupt {
				if cl.Orphan {
					orphans = append(orphans, cl)
				}
			}
			if got := len(orphans) == 1; got != tc.orphan {
				t.Fatalf("orphan detection on %q = %v (corrupt %+v), want %v", tc.line, got, corrupt, tc.orphan)
			}
			if tc.orphan && orphans[0].Bench != "BenchmarkAnchor" {
				t.Errorf("orphan attributed to %q, want the preceding BenchmarkAnchor", orphans[0].Bench)
			}
		})
	}
}

// TestAuditStreamSampleFloor pins the per-benchmark floor (spec §9): every
// result row carries exactly the demanded count, deviation in either direction
// refuses the benchmark, attributed corruption refuses even a count-exact
// benchmark (a spliced line that still parsed replaced a genuine sample), and
// an orphaned tail attributable to no selected benchmark refuses the package.
func TestAuditStreamSampleFloor(t *testing.T) {
	selected := []string{"BenchmarkA", "BenchmarkB"}
	rows := func(spec map[string]int) []*benchfmt.Result {
		var out []*benchfmt.Result
		for name, n := range spec {
			for i := 0; i < n; i++ {
				out = append(out, &benchfmt.Result{Name: benchfmt.Name(name)})
			}
		}
		return out
	}
	t.Run("exact counts pass", func(t *testing.T) {
		audit := AuditStream(rows(map[string]int{"A-8": 2, "A/sub-8": 2, "B-8": 2}), nil, 2, selected)
		if len(audit.Refused) != 0 || audit.PackageCause != "" {
			t.Fatalf("clean stream refused: %+v", audit)
		}
	})
	t.Run("deficit refuses only the deficient benchmark", func(t *testing.T) {
		audit := AuditStream(rows(map[string]int{"A-8": 2, "A/sub-8": 1, "B-8": 2}), nil, 2, selected)
		if len(audit.Refused) != 1 || len(audit.Refused["BenchmarkA"]) != 1 {
			t.Fatalf("refused = %+v, want only BenchmarkA", audit.Refused)
		}
	})
	t.Run("surplus refuses", func(t *testing.T) {
		audit := AuditStream(rows(map[string]int{"A-8": 3, "B-8": 2}), nil, 2, selected)
		if len(audit.Refused["BenchmarkA"]) != 1 {
			t.Fatalf("surplus row not refused: %+v", audit.Refused)
		}
	})
	t.Run("attributed corruption refuses a count-exact benchmark", func(t *testing.T) {
		corrupt := []CorruptLine{{Line: 7, Text: "x", Cause: "c", Bench: "BenchmarkA"}}
		audit := AuditStream(rows(map[string]int{"A-8": 2, "B-8": 2}), corrupt, 2, selected)
		if len(audit.Refused["BenchmarkA"]) != 1 || len(audit.Refused) != 1 {
			t.Fatalf("refused = %+v, want only BenchmarkA", audit.Refused)
		}
		if audit.PackageCause != "" {
			t.Fatalf("attributable corruption raised package cause %q", audit.PackageCause)
		}
	})
	t.Run("unattributable orphan refuses the package", func(t *testing.T) {
		corrupt := []CorruptLine{{Line: 7, Text: "1 2 ns/op", Cause: "orphaned", Orphan: true}}
		audit := AuditStream(rows(map[string]int{"A-8": 2, "B-8": 2}), corrupt, 2, selected)
		if audit.PackageCause == "" {
			t.Fatal("unattributable orphan did not refuse the package")
		}
	})
	t.Run("orphan attributed outside the selection refuses the package", func(t *testing.T) {
		corrupt := []CorruptLine{{Line: 7, Text: "1 2 ns/op", Cause: "orphaned", Bench: "BenchmarkElsewhere", Orphan: true}}
		audit := AuditStream(rows(map[string]int{"A-8": 2}), corrupt, 2, selected)
		if audit.PackageCause == "" {
			t.Fatal("orphan outside the selection did not refuse the package")
		}
	})
	t.Run("unattributable non-orphan line is not a refusal", func(t *testing.T) {
		corrupt := []CorruptLine{{Line: 7, Text: "Benchmarking things", Cause: "missing iteration count", Bench: "Benchmarking"}}
		audit := AuditStream(rows(map[string]int{"A-8": 2, "B-8": 2}), corrupt, 2, selected)
		if len(audit.Refused) != 0 || audit.PackageCause != "" {
			t.Fatalf("junk skipped line escalated: %+v", audit)
		}
	})
	t.Run("deficit outside the selection is ignored", func(t *testing.T) {
		audit := AuditStream(rows(map[string]int{"A-8": 2, "Elsewhere-8": 1}), nil, 2, selected)
		if len(audit.Refused) != 0 {
			t.Fatalf("unselected deficit refused: %+v", audit.Refused)
		}
	})
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
			if _, _, _, err := Parse(out); err == nil {
				t.Fatalf("reserved configuration %q accepted", line)
			}
		})
	}
}
