package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"io"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/pew/internal/gotool"
	"github.com/greatliontech/pew/internal/recordingtest"
	runpkg "github.com/greatliontech/pew/internal/run"
	"golang.org/x/perf/benchfmt"
)

func TestABPackageSelectsBenchmarkOnlySingleCountProcesses(t *testing.T) {
	const pattern = "^BenchmarkSelected$/case"
	dirA, dirB := t.TempDir(), t.TempDir()
	fp := recordingtest.Defaults().Fingerprint
	prep := &abPreparation{pkg: pkgMeta{ImportPath: "example.com/selection", Dir: dirA}, sideBPkgDir: dirB, binA: "binary-A", binB: "binary-B", guardsA: fp.Guards, guardsB: fp.Guards}
	var order []string
	ac := abConfig{bench: pattern, count: 3, benchtime: "1x", ref: "baseline", throttle: func() runpkg.ThrottleSnapshot { return runpkg.ThrottleSnapshot{} }}
	ac.execute = func(dir, pin string, env []string, binary string, args []string) ([]byte, error) {
		t.Helper()
		// Model the test binary's flag parser, including the significant effect
		// of an empty positional argument: parsing stops before later flags.
		flags := flag.NewFlagSet("test binary", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		run := flags.String("test.run", "", "")
		bench := flags.String("test.bench", "", "")
		count := flags.Int("test.count", 1, "")
		mem := flags.Bool("test.benchmem", false, "")
		duration := flags.String("test.benchtime", "1s", "")
		if err := flags.Parse(args); err != nil {
			t.Fatal(err)
		}
		if *run != "^$" || *bench != pattern || *count != 1 || !*mem || *duration != "1x" || flags.NArg() != 0 {
			t.Fatalf("wrong measured process selection: args=%q run=%q bench=%q count=%d mem=%t duration=%q positional=%q", args, *run, *bench, *count, *mem, *duration, flags.Args())
		}
		wantDir, wantBinary := dirA, "binary-A"
		if len(order)%2 != 0 {
			wantDir, wantBinary = dirB, "binary-B"
		}
		if dir != wantDir || binary != wantBinary {
			t.Fatalf("pair order: %s %s, want %s %s", dir, binary, wantDir, wantBinary)
		}
		order = append(order, binary)
		return []byte("pkg: example.com/selection\nBenchmarkSelected/case-2 1 42 ns/op 8 B/op 1 allocs/op\n"), nil
	}
	var out bytes.Buffer
	if err := abPackage(t.Context(), &out, io.Discard, ac, prep, 1, 1, newEnvironments(testEnvironment(t, nil), runpkg.Pin{})); err != nil {
		t.Fatal(err)
	}
	if len(order) != 6 || !strings.Contains(out.String(), "Selected/case-2") {
		t.Fatalf("missing paired measurements: %v\n%s", order, &out)
	}
}

func TestExplainDynamicStrategyNamesFullDistinctValues(t *testing.T) {
	const before = "gofresh/dynamic-state@123456789 old derivation"
	const after = "gofresh/dynamic-state@123456789 new derivation"
	for _, current := range []string{before, after} {
		fp := recordingtest.Defaults().Fingerprint
		fp.RuntimeInputs, fp.RuntimeDigest = "", ""
		fp.DynamicStateStrategy = before
		cur := fp
		cur.DynamicStateStrategy = current
		var out bytes.Buffer
		if err := explainCapturedRecord(t.Context(), &out, t.TempDir(), fp, cur, nil); err != nil {
			t.Fatal(err)
		}
		var rows []string
		for _, line := range strings.Split(out.String(), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "dynamic-state strategy ") {
				rows = append(rows, line)
			}
		}
		match := "yes"
		if current != before {
			match = "NO"
		}
		if len(rows) != 1 || !strings.Contains(rows[0], strconv.Quote(before)) || !strings.Contains(rows[0], strconv.Quote(current)) || !strings.HasSuffix(strings.TrimSpace(rows[0]), match) {
			t.Fatalf("strategy explanation lost its named, unabridged judgment:\n%s", &out)
		}
	}
}

func TestAdmissionFormatRefusalAcrossRows(t *testing.T) {
	for _, working := range []bool{false, true} {
		valid := func() []*benchfmt.Result {
			return []*benchfmt.Result{admissionRow(admissionConfig(gofresh.DynamicStateStrategy)), admissionRow(admissionConfig(gofresh.DynamicStateStrategy)), admissionRow(admissionConfig(gofresh.DynamicStateStrategy))}
		}
		if a := admitRecording(valid(), working); !a.ok {
			t.Fatalf("valid control refused: %+v", a)
		}
		for index := 0; index < 3; index++ {
			for _, reason := range []string{"legacy version", "raw annotation", "annotation before false"} {
				rows := valid()
				row := rows[index]
				switch reason {
				case "legacy version":
					for j := range row.Config {
						if row.Config[j].Key == runpkg.KeyFormat.Name {
							row.Config[j].Value = []byte("3")
						}
					}
				case "raw annotation":
					row.Config = append(row.Config, benchfmt.Config{Key: runpkg.FormatInvalidAnnotation, Value: []byte("true")})
				case "annotation before false":
					row.Config = append([]benchfmt.Config{{Key: runpkg.FormatInvalidAnnotation, Value: []byte("true")}}, row.Config...)
					row.Config = append(row.Config, benchfmt.Config{Key: runpkg.FormatInvalidAnnotation, Value: []byte("false")})
				}
				a := admitRecording(rows, working)
				if a.ok || a.class != "format" || a.fp != (gofresh.Fingerprint{}) || a.ledger != "" {
					t.Fatalf("working=%t row=%d %s admitted evidence: %+v", working, index, reason, a)
				}
			}
		}
	}
}

func TestInheritedMalformedEnvironmentRefusesBeforeListing(t *testing.T) {
	const modeKey = "PEW_ENVIRONMENT_REFUSAL_CHILD"
	const malformed = "PEW_INHERITED_ENTRY_WITHOUT_EQUALS"
	if mode := os.Getenv(modeKey); mode != "" {
		verb, invalid, _ := strings.Cut(mode, ":")
		if slices.Contains(os.Environ(), malformed) != (invalid == "invalid") {
			t.Fatal("child did not receive the intended raw environment")
		}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		ctx, stop := startReporter(ctx, io.Discard, 0)
		defer stop()
		listed := false
		setPhaseHook(ctx, func(phase string) {
			if phase == "listing" {
				listed = true
				cancel()
			}
		})
		var err error
		switch verb {
		case "ab":
			err = runAB(ctx, io.Discard, io.Discard, abConfig{count: 1}, []string{"."})
		case "gc":
			err = runGC(ctx, io.Discard, "")
		default:
			t.Fatal(mode)
		}
		var refused *gotool.EnvironmentError
		if invalid == "invalid" {
			if !errors.As(err, &refused) || listed {
				t.Fatalf("%s lost entry-time environment refusal: listed=%t err=%v", verb, listed, err)
			}
		} else {
			var interrupted *interruptedError
			if errors.As(err, &refused) || !listed || !errors.As(err, &interrupted) {
				t.Fatalf("%s valid control failed to reach listing: listed=%t err=%v", verb, listed, err)
			}
		}
		return
	}
	if runtime.GOOS != "linux" {
		t.Skip("raw malformed exec environment witness is Linux-audited")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, verb := range []string{"ab", "gc"} {
		for _, kind := range []string{"valid", "invalid"} {
			t.Run(verb+"/"+kind, func(t *testing.T) {
				cmd := exec.CommandContext(t.Context(), binary, "-test.run=^TestInheritedMalformedEnvironmentRefusesBeforeListing$")
				cmd.Env = append(os.Environ(), modeKey+"="+verb+":"+kind)
				if kind == "invalid" {
					cmd.Env = append(cmd.Env, malformed)
				}
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("environment child: %v\n%s", err, output)
				}
			})
		}
	}
}
