package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	git "github.com/go-git/go-git/v5"
	pprof "github.com/google/pprof/profile"
	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/runtimeinput"
	"github.com/greatliontech/pew/internal/gotool"
	"github.com/greatliontech/pew/internal/profiles"
	"github.com/greatliontech/pew/internal/recordingtest"
	runpkg "github.com/greatliontech/pew/internal/run"
	"github.com/greatliontech/pew/internal/store"
)

func TestDiagnosticChildrenRefuseMissingCorruptAndNonfinite(t *testing.T) {
	for _, raw := range []string{"PASS\n", "BenchmarkOther-8 1 1 ns/op\n", "BenchmarkWork-8 1 NaN ns/op\n", "BenchmarkWork-8 1 +Inf ns/op\n", "BenchmarkWork-8 corrupt\n", "BenchmarkWork-8 1 1 ns/op\nBenchmarkWork-8 1 1 ns/op\n"} {
		if _, err := diagnosticChildren([]byte(raw+"PASS\n"), "BenchmarkWork"); err == nil {
			t.Fatalf("admitted %q", raw)
		}
	}
	if _, err := diagnosticChildren([]byte("BenchmarkWork/child-8 9 1 ns/op\n"), "BenchmarkWork"); err == nil {
		t.Fatal("uncompleted harness accepted")
	}
	got, err := diagnosticChildren([]byte("BenchmarkWork/child-8 9 1 ns/op\nPASS\n"), "BenchmarkWork")
	if err != nil || len(got) != 1 || got[0].Iterations != 9 || got[0].Name != "Work/child-8" {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestBrokenCompanionDoesNotRefuseMeasurement(t *testing.T) {
	rows := recordingtest.Results("X", []float64{1, 2})
	for _, r := range rows {
		r.SetConfig(runpkg.KeyProfiles.Name, "broken")
	}
	adm := admitRecording(rows, true)
	if !adm.ok {
		t.Fatalf("profile fault invalidated measurement: %+v", adm)
	}
	status, err := profileStatuses(t.Context(), store.New(t.TempDir()), adm, nil, "p", "BenchmarkX")
	if err == nil || len(status) != 1 || status[0].Integrity != "invalid" {
		t.Fatalf("fault became absent: %+v %v", status, err)
	}
	rows[1].SetConfig(runpkg.KeyProfiles.Name, "different")
	if !admitRecording(rows, true).ok {
		t.Fatal("index disagreement invalidated measurement")
	}
	if _, _, err := profiles.FromRows(rows); err == nil {
		t.Fatal("mixed index admitted")
	}
}

func TestProfileFlagsAreOptIn(t *testing.T) {
	cmd := newRunCmd()
	if cmd.Flags().Lookup("profile").DefValue != "" || cmd.Flags().Lookup("profile-benchtime").DefValue != "1s" {
		t.Fatal("changed opt-in defaults")
	}
	if err := cmd.Flags().Parse([]string{"--profile"}); err != nil {
		t.Fatal(err)
	}
	if got, _ := cmd.Flags().GetString("profile"); got != "cpu,alloc" {
		t.Fatal(got)
	}
}

func TestRealProfilesAreIndependentAndServeMeasurement(t *testing.T) {
	if testing.Short() {
		t.Skip("executes real CPU/allocation diagnostics and native source views")
	}
	root := twoArmFixture(t)
	writeFile(t, filepath.Join(root, "bench_test.go"), `package arms
import "testing"
var sink []byte
var number uint64
//gofresh:pure
func BenchmarkFirst(b *testing.B) {
 for i:=0;i<b.N;i++ {
  sink=make([]byte, 128<<10)
  for j:=0;j<4096;j++ { number = number*1664525+uint64(j)+1013904223 }
 }
}
`)
	repo, err := git.PlainOpen(root)
	if err != nil {
		t.Fatal(err)
	}
	commitAll(t, repo, "profile fixture")
	withWorkingDir(t, root)
	st := store.New(filepath.Join(root, "benchmarks"))
	rc := runConfig{benchDir: st.Root, opts: runpkg.Options{Bench: "^BenchmarkFirst$", Count: 2, Benchtime: "1x"}}
	var stdout, stderr bytes.Buffer
	if err := runRun(t.Context(), &stdout, &stderr, rc, []string{"."}); err != nil {
		t.Fatalf("measure: %v\n%s", err, &stderr)
	}
	before, err := st.Read("", "BenchmarkFirst", "")
	if err != nil {
		t.Fatal(err)
	}
	measurement := admitRecording(before, true)
	revision := profiles.Revision(before)
	if _, present, err := profiles.FromRows(before); err != nil || present {
		t.Fatal("unrequested diagnostics present")
	}
	rc.profile, rc.profileBenchtime = "cpu,alloc", "500ms"
	rc.execute = func(dir, pin string, env, args []string) ([]byte, error) {
		build := false
		for _, a := range args {
			if a == "-c" {
				build = true
			}
		}
		if !build {
			t.Fatalf("served measurement reran: %v", args)
		}
		e, err := gotool.NewEnvironment(env)
		if err != nil {
			return nil, err
		}
		return runpkg.ExecuteContext(t.Context(), dir, pin, e, args)
	}
	stdout.Reset()
	stderr.Reset()
	if err := runRun(t.Context(), &stdout, &stderr, rc, []string{"."}); err != nil {
		t.Fatalf("profiles: %v\n%s\n%s", err, &stdout, &stderr)
	}
	after, err := st.Read("", "BenchmarkFirst", "")
	if err != nil {
		t.Fatal(err)
	}
	index, present, err := profiles.FromRows(after)
	if err != nil || !present || len(index.Captures) != 2 {
		t.Fatalf("index: %+v %v %v", index, present, err)
	}
	if profiles.Revision(after) != revision || admitRecording(after, true).fp != measurement.fp || len(after) != len(before) {
		t.Fatal("diagnostics changed statistical evidence")
	}
	for _, c := range index.Captures {
		manifest, err := base64.RawURLEncoding.DecodeString(c.Fingerprint.RuntimeInputs)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(manifest, &m); err != nil {
			t.Fatal(err)
		}
		if _, ok := m["outcome"]; ok {
			t.Fatalf("profiler acquired unsupported outcomes: %s", manifest)
		}
		b, err := st.ReadObject(c.SHA256, c.Size)
		if err != nil {
			t.Fatal(err)
		}
		metrics, err := profiles.Analyze(b, c.Kind, c.Sources)
		if err != nil || profiles.Empty(metrics) {
			t.Fatalf("%s not useful: %v", c.Kind, err)
		}
		if len(c.Children) != 1 || c.Children[0].Iterations < 1 || c.MeasurementRevision != revision || len(c.Sources) == 0 {
			t.Fatalf("missing diagnostic provenance: %+v", c)
		}
	}
	if !strings.Contains(stderr.String(), profiles.UnsupportedReason) {
		t.Fatal("unsupported model not reported")
	}
	stdout.Reset()
	if err := runStatus(t.Context(), &stdout, st.Root, "", false, true, false, []string{"."}); err != nil {
		t.Fatalf("status: %v\n%s", err, &stdout)
	}
	if !strings.Contains(stdout.String(), "integrity=verified") || !strings.Contains(stdout.String(), "relation=unverified") || !strings.Contains(stdout.String(), "flat=") {
		t.Fatalf("missing profile judgments: %s", &stdout)
	}
	for n, mode := range []string{"failure", "missing-output", "empty", "corrupt", "source-drift", "executable-changed", "executable-missing", "cancel-in-flight", "cancel-completed", "concurrent-replacement"} {
		t.Run(mode, func(t *testing.T) {
			key := store.Key{Bench: "BenchmarkFirst"}
			prior, err := st.ReadBytes(key)
			if err != nil {
				t.Fatal(err)
			}
			fixture, err := os.ReadFile(filepath.Join(root, "bench_test.go"))
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			testRC := rc
			testRC.profile = "cpu"
			testRC.profileBenchtime = fmt.Sprintf("%dms", 400+n*10)
			var concurrent []byte
			testRC.diagnostic = func(c context.Context, dir, pin string, env gotool.Environment, bin string, args []string, diagnostics io.Writer) ([]byte, error) {
				switch mode {
				case "failure":
					return nil, fmt.Errorf("profiler startup failed")
				case "missing-output":
					fmt.Fprintln(diagnostics, "profiling startup refused")
					return []byte("BenchmarkFirst-8 1 1 ns/op\n"), nil
				case "cancel-in-flight":
					cancel()
					return nil, context.Canceled
				case "empty", "corrupt":
					var data bytes.Buffer
					p := &pprof.Profile{SampleType: []*pprof.ValueType{{Type: "cpu", Unit: "nanoseconds"}}}
					if err := p.Write(&data); err != nil {
						t.Fatal(err)
					}
					if mode == "corrupt" {
						data.Reset()
						data.WriteString("broken profile")
					}
					for _, arg := range args {
						if path, ok := strings.CutPrefix(arg, "-test.cpuprofile="); ok {
							if err := os.WriteFile(path, data.Bytes(), 0600); err != nil {
								t.Fatal(err)
							}
						}
					}
					return []byte("BenchmarkFirst-8 1 1 ns/op\nPASS\n"), nil
				}
				out, err := runpkg.ExecuteDiagnostic(c, dir, pin, env, bin, args, diagnostics)
				if err != nil {
					return nil, err
				}
				switch mode {
				case "source-drift":
					writeFile(t, filepath.Join(root, "bench_test.go"), string(fixture)+"\nfunc AddedSource() {}\n")
				case "executable-changed":
					if err := os.WriteFile(bin, []byte("different regular executable bytes"), 0600); err != nil {
						t.Fatal(err)
					}
				case "executable-missing":
					if err := os.Remove(bin); err != nil {
						t.Fatal(err)
					}
				case "cancel-completed":
					cancel()
				case "concurrent-replacement":
					rows, err := st.Read("", key.Bench, "")
					if err != nil {
						t.Fatal(err)
					}
					rows[0].Iters++
					if err := st.Write("", key.Bench, "", rows); err != nil {
						t.Fatal(err)
					}
					concurrent, err = st.ReadBytes(key)
					if err != nil {
						t.Fatal(err)
					}
				}
				return out, nil
			}
			var out, log bytes.Buffer
			err = runRun(ctx, &out, &log, testRC, []string{"."})
			if err == nil {
				t.Fatalf("%s accepted; %s %s", mode, &out, &log)
			}
			back, readErr := st.ReadBytes(key)
			if readErr != nil {
				t.Fatal(readErr)
			}
			switch mode {
			case "cancel-completed":
				var interrupted *interruptedError
				if !errors.As(err, &interrupted) || bytes.Equal(prior, back) || !strings.Contains(out.String(), "profiled") {
					t.Fatalf("completed profile lost: %v %s %s", err, &out, &log)
				}
			case "concurrent-replacement":
				if !bytes.Equal(back, concurrent) || !strings.Contains(out.String(), "recording changed") {
					t.Fatalf("clobbered concurrent replacement: %v %s", err, &out)
				}
			default:
				if !bytes.Equal(prior, back) {
					t.Fatalf("%s changed prior recording", mode)
				}
			}
			if mode == "missing-output" && (!strings.Contains(log.String(), "profiling startup refused") || strings.Contains(out.String(), "empty cpu")) {
				t.Fatalf("startup failure became empty: %s %s", &out, &log)
			}
			if mode == "source-drift" {
				writeFile(t, filepath.Join(root, "bench_test.go"), string(fixture))
			}
			if mode == "executable-changed" && (!strings.Contains(out.String(), "profile: executable changed during diagnostic") || strings.Contains(out.String(), "%!w")) {
				t.Fatalf("misreported byte mismatch: %v\n%s", err, &out)
			}
			if mode == "executable-missing" && !strings.Contains(out.String(), "profile: reading executable after diagnostic:") {
				t.Fatalf("misreported read failure: %v\n%s", err, &out)
			}
		})
	}
	// A corrupt companion is an incomplete report, never a lost measurement verdict.
	p, _ := st.ObjectPath(index.Captures[0].SHA256)
	if err := os.WriteFile(p, []byte("corrupt"), 0600); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	if err := runStatus(t.Context(), &stdout, st.Root, "", false, false, true, []string{"."}); err == nil {
		t.Fatal("corruption reported success")
	}
	if !strings.Contains(stdout.String(), `"verdict":"valid"`) || !strings.Contains(stdout.String(), `"profiles"`) || !strings.Contains(stdout.String(), "integrity failure") {
		t.Fatalf("measurement disappeared: %s", &stdout)
	}
}

func TestProfileRuntimeRelationRefusesKnownMovement(t *testing.T) {
	root := t.TempDir()
	before := testEnvironment(t, []string{"PEW_PROFILE_INPUT=before"})
	bracket, err := runtimeinput.CaptureBracket(t.Context(), root, []string{root})
	if err != nil {
		t.Fatal(err)
	}
	obs, err := runtimeinput.FromTestLog([]byte("# test log\ngetenv PEW_PROFILE_INPUT\n"), root, root, before.Values(), runtimeinput.WithCompletedProcess("fixture"), runtimeinput.WithBracket(bracket))
	if err != nil {
		t.Fatal(err)
	}
	fp := gofresh.Fingerprint{RuntimeInputs: obs.Manifest, RuntimeDigest: obs.Digest}
	if err := profileInputRelation(t.Context(), fp, root, root, before); err != nil {
		t.Fatal(err)
	}
	after := testEnvironment(t, []string{"PEW_PROFILE_INPUT=after"})
	if err := profileInputRelation(t.Context(), fp, root, root, after); err == nil {
		t.Fatal("runtime mismatch attached")
	}
}

func TestUnrequestedProfilesDoNoWork(t *testing.T) {
	if err := runProfiles(context.Background(), io.Discard, io.Discard, runConfig{}, nil, nil, environments{}, runpkg.Conditions{}, nil, []string{"BenchmarkX"}); err != nil {
		t.Fatal(err)
	}
}

func TestProfileOnlyAfterInertGrowthPreservesProducingEvidence(t *testing.T) {
	if testing.Short() {
		t.Skip("executes a real allocation profile after native applicability extension")
	}
	root := twoArmFixture(t)
	writeFile(t, filepath.Join(root, "work.go"), "package arms\nfunc AllocationSize() int { return 128 << 10 }\n")
	writeFile(t, filepath.Join(root, "bench_test.go"), `package arms
import "testing"
var sink []byte
//gofresh:pure
func BenchmarkFirst(b *testing.B) { for i:=0;i<b.N;i++ { sink=make([]byte, AllocationSize()) } }
`)
	repo, err := git.PlainOpen(root)
	if err != nil {
		t.Fatal(err)
	}
	commitAll(t, repo, "measured source")
	withWorkingDir(t, root)
	st := store.New(filepath.Join(root, "benchmarks"))
	rc := runConfig{benchDir: st.Root, opts: runpkg.Options{Bench: "^BenchmarkFirst$", Count: 2, Benchtime: "1x"}}
	var out, log bytes.Buffer
	if err := runRun(t.Context(), &out, &log, rc, []string{"."}); err != nil {
		t.Fatalf("measure: %v\n%s", err, &log)
	}
	before, err := st.Read("", "BenchmarkFirst", "")
	if err != nil {
		t.Fatal(err)
	}
	original := admitRecording(before, true).fp
	writeFile(t, filepath.Join(root, "extra_test.go"), "package arms\nfunc UncalledExtra() int { return 1 }\n")
	rc.profile, rc.profileBenchtime = "alloc", "200ms"
	rc.execute = func(dir, pin string, env, args []string) ([]byte, error) {
		if len(args) < 2 || args[0] != "test" || args[1] != "-c" {
			t.Fatalf("statistical remeasurement after inert growth: %v", args)
		}
		e, err := gotool.NewEnvironment(env)
		if err != nil {
			return nil, err
		}
		return runpkg.ExecuteContext(t.Context(), dir, pin, e, args)
	}
	out.Reset()
	log.Reset()
	if err := runRun(t.Context(), &out, &log, rc, []string{"."}); err != nil {
		t.Fatalf("profile-only growth: %v\n%s\n%s", err, &out, &log)
	}
	after, err := st.Read("", "BenchmarkFirst", "")
	if err != nil {
		t.Fatal(err)
	}
	measured := admitRecording(after, true).fp
	if measured.InertTestVariantApplicability.Strategy != gofresh.InertTestVariantExtension || measured.EffectiveTestVariantClosure() == original.TestVariantClosure {
		t.Fatalf("missing proved extension: %+v", measured)
	}
	producing := measured
	producing.InertTestVariantApplicability = gofresh.InertTestVariantApplicability{}
	if producing != original {
		t.Fatal("original producing fingerprint or observation support changed")
	}
	if len(before) != len(after) {
		t.Fatal("statistical rows changed")
	}
	for i := range before {
		if !bytes.Equal(before[i].Name, after[i].Name) || before[i].Iters != after[i].Iters || !reflect.DeepEqual(before[i].Values, after[i].Values) {
			t.Fatal("statistical sample changed")
		}
	}
	index, present, err := profiles.FromRows(after)
	if err != nil || !present || len(index.Captures) != 1 {
		t.Fatalf("no profile attached: %+v %v", index, err)
	}
	capture := index.Captures[0]
	if capture.Fingerprint.TestVariantClosure != measured.EffectiveTestVariantClosure() || capture.Fingerprint.TestVariantClosure == original.TestVariantClosure || capture.Fingerprint.InertTestVariantApplicability != (gofresh.InertTestVariantApplicability{}) {
		t.Fatal("diagnostic producing variant was rewritten or did not describe its build")
	}
	if _, err := st.ReadObject(capture.SHA256, capture.Size); err != nil {
		t.Fatal(err)
	}
	// The read/serve path must consume the same proof as attachment.
	rc.diagnostic = func(context.Context, string, string, gotool.Environment, string, []string, io.Writer) ([]byte, error) {
		t.Fatal("fresh diagnostic reran after attachment")
		return nil, nil
	}
	out.Reset()
	if err := runRun(t.Context(), &out, &log, rc, []string{"."}); err != nil || !strings.Contains(out.String(), "profile-served") {
		t.Fatalf("serve: %v\n%s", err, &out)
	}
	check := func(fp gofresh.Fingerprint, c profiles.Capture) error {
		e, _, err := newEngineAt(t.Context(), root, root, false, testEnvironment(t, os.Environ()))
		if err != nil {
			t.Fatal(err)
		}
		v, err := e.NewViewFor(t.Context(), []gofresh.Subject{{Package: c.Package, Symbol: c.Benchmark}}, root, gofresh.Measurement)
		if err != nil {
			t.Fatal(err)
		}
		return profiles.CheckedRelation(t.Context(), c, after, fp, v)
	}
	if err := check(measured, capture); err != nil {
		t.Fatal(err)
	}
	unknown := measured
	unknown.InertTestVariantApplicability.Strategy = "unknown-future-strategy"
	if err := check(unknown, capture); err == nil {
		t.Fatal("unknown strategy accessor conferred applicability")
	}
	// A same-span change cannot escape the checking view's validation.
	e, _, err := newEngineAt(t.Context(), root, root, false, testEnvironment(t, os.Environ()))
	if err != nil {
		t.Fatal(err)
	}
	v, err := e.NewViewFor(t.Context(), []gofresh.Subject{{Package: capture.Package, Symbol: capture.Benchmark}}, root, gofresh.Measurement)
	if err != nil {
		t.Fatal(err)
	}
	// An operational proof failure belongs to the error channel, not pair mismatch.
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	failed, err := inspectProfile(cancelled, st, capture, after, v)
	if !errors.Is(err, context.Canceled) || failed.Error == "" || failed.Relation != "unverified" {
		t.Fatalf("operational failure misclassified: %+v %v", failed, err)
	}
	writeFile(t, filepath.Join(root, "extra_test.go"), "package arms\nfunc UncalledExtra() int { return 2 }\n")
	if err := profiles.CheckedRelation(t.Context(), capture, after, measured, v); err == nil {
		t.Fatal("moved checking view conferred applicability")
	}
	if err := check(measured, capture); err == nil {
		t.Fatal("non-inert variant drift conferred applicability")
	}
	// Later production movement stales both historical executions. It does not
	// contradict their pair relationship, and ordinary status must still succeed.
	writeFile(t, filepath.Join(root, "extra_test.go"), "package arms\nfunc UncalledExtra() int { return 1 }\n")
	writeFile(t, filepath.Join(root, "work.go"), "package arms\nfunc AllocationSize() int { return 256 << 10 }\n")
	if err := check(measured, capture); !errors.Is(err, profiles.ErrRelationUnproven) || errors.Is(err, profiles.ErrRelationMismatch) {
		t.Fatalf("stale proof must refuse attachment without asserting pair mismatch: %v", err)
	}
	priorBytes, err := st.ReadBytes(store.Key{Bench: "BenchmarkFirst"})
	if err != nil {
		t.Fatal(err)
	}
	priorBlob, err := st.ReadObject(capture.SHA256, capture.Size)
	if err != nil {
		t.Fatal(err)
	}
	for _, jsonOut := range []bool{false, true} {
		var status bytes.Buffer
		if err := runStatus(t.Context(), &status, st.Root, "", false, !jsonOut, jsonOut, []string{"."}); err != nil {
			t.Fatalf("ordinary stale profile failed status: %v\n%s", err, &status)
		}
		if jsonOut {
			var row statusJSONRow
			if err := json.Unmarshal(status.Bytes(), &row); err != nil {
				t.Fatal(err)
			}
			if row.Verdict != "stale" || row.Error != "" || len(row.Profiles) != 1 || row.Profiles[0].Freshness != "stale" || row.Profiles[0].Relation != "unverified" || row.Profiles[0].Error != "" || !strings.Contains(row.Profiles[0].Reason, "applicability not proven") {
				t.Fatalf("stale executions reported pair disagreement: %s", &status)
			}
		} else if !strings.Contains(status.String(), "freshness=stale relation=unverified") || !strings.Contains(status.String(), "applicability not proven") {
			t.Fatalf("text loses unproven relation: %s", &status)
		}
	}
	// Contradictions between the saved pair remain mismatches even when no current
	// applicability proof is available. They do not become operational errors.
	for _, mutate := range []func(*profiles.Capture){
		func(c *profiles.Capture) { c.MeasurementRevision = profiles.Digest([]byte("other measurement")) },
		func(c *profiles.Capture) { c.Children = []profiles.Child{{Name: "First/other-8", Iterations: 1}} },
		func(c *profiles.Capture) { c.Fingerprint.Guards.BuildConfig += "different" },
	} {
		bad := capture
		mutate(&bad)
		row, err := inspectProfile(t.Context(), st, bad, after, nil)
		if err != nil || row.Relation != "mismatch" || row.Error != "" {
			t.Fatalf("pair disagreement misclassified: %+v %v", row, err)
		}
	}
	currentBytes, err := st.ReadBytes(store.Key{Bench: "BenchmarkFirst"})
	if err != nil {
		t.Fatal(err)
	}
	currentBlob, err := st.ReadObject(capture.SHA256, capture.Size)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(priorBytes, currentBytes) || !bytes.Equal(priorBlob, currentBlob) {
		t.Fatal("status changed historical artifacts")
	}
	// Neither refusal is allowed to alter either historical producing record.
	back, err := st.Read("", "BenchmarkFirst", "")
	if err != nil {
		t.Fatal(err)
	}
	if profiles.Revision(back) != profiles.Revision(after) || back[0].GetConfig(runpkg.KeyProfiles.Name) != after[0].GetConfig(runpkg.KeyProfiles.Name) {
		t.Fatal("relation checks rewrote historical evidence")
	}
}
