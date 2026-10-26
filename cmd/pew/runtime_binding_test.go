package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh"
	gofreshtool "github.com/greatliontech/gofresh/gotool"
	"github.com/greatliontech/gofresh/runtimeinput"
)

// The compiler's complete environment and the retained program's actual inputs
// have separate owners. A second runtime binding can use the first artifact,
// but neither its guards nor its observation can become the first run's facts.
func TestRuntimeBindingsPreserveBuildAndProcessProvenance(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and executes a retained artifact under distinct runtime bindings")
	}
	t.Parallel()
	root := t.TempDir()
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	pkgDir := filepath.Join(root, "subject")
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/runtimebindings\n\ngo 1.26\n")
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(pkgDir, "bench_test.go"), `package subject
import ("os"; "testing")
//gofresh:pure
func BenchmarkInputs(b *testing.B) {
 value := os.Getenv("PEW_BINDING_VALUE")
 if value != "first" && value != "second" { b.Fatal("wrong runtime environment", value) }
 pwd := os.Getenv("PWD")
 wd, err := os.Getwd()
 if err != nil || pwd != wd { b.Fatal("wrong runtime PWD", pwd, wd, err) }
 data, err := os.ReadFile("input.txt")
 if err != nil || string(data) != "runtime file" { b.Fatal("wrong runtime directory", err) }
}
`)
	writeFile(t, filepath.Join(pkgDir, "input.txt"), "runtime file")
	buildEnv := testEnvironment(t, gofreshtool.SetEnv(os.Environ(), "PEW_BINDING_VALUE", "compiler-only")).WithParallelism(3)
	firstEnv := testEnvironment(t, gofreshtool.SetEnv(buildEnv.Values(), "PEW_BINDING_VALUE", "first")).WithParallelism(1)
	secondEnv := testEnvironment(t, gofreshtool.SetEnv(buildEnv.Values(), "PEW_BINDING_VALUE", "second")).WithParallelism(2)
	ctx := t.Context()
	session, err := buildEngine(ctx, root, pkgDir, buildEnv, &firstEnv, "")
	if err != nil {
		t.Fatal(err)
	}
	subject := gofresh.Subject{Package: "example.com/runtimebindings/subject", Symbol: "BenchmarkInputs"}
	first, err := newViewFor(session, ctx, []gofresh.Subject{subject}, root, gofresh.Measurement)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := first.Snapshot()
	build, err := snapshot.BuildDescription(subject.Package)
	if err != nil {
		t.Fatal(err)
	}
	wantBuild, err := buildEnv.For(root)
	if err != nil {
		t.Fatal(err)
	}
	// Gofresh pins the ordinary loader as part of the complete build description.
	wantBuild = gofreshtool.SetEnv(wantBuild, "GOPACKAGESDRIVER", "off")
	if build.Directory != root || !slices.Equal(build.Environment, wantBuild) {
		t.Fatalf("compilation used runtime configuration: directory=%q, environment=%v; want %q, %v", build.Directory, build.Environment, root, wantBuild)
	}
	second, err := snapshot.BindRuntime(ctx, gofresh.RuntimeDescription{Directory: pkgDir, Environment: secondEnv.Values()})
	if err != nil {
		t.Fatal(err)
	}
	if second.Snapshot() != snapshot {
		t.Fatal("runtime binding replaced the source/build generation")
	}
	firstFacts, err := first.CurrentFacts(ctx, subject)
	if err != nil {
		t.Fatal(err)
	}
	secondFacts, err := second.CurrentFacts(ctx, subject)
	if err != nil {
		t.Fatal(err)
	}
	if firstFacts.Guards.BuildConfig == "" || firstFacts.Guards.BuildConfig != secondFacts.Guards.BuildConfig || firstFacts.MaximalClosure != secondFacts.MaximalClosure {
		t.Fatalf("runtime binding changed source/build facts: first=%+v, second=%+v", firstFacts, secondFacts)
	}
	if firstFacts.Guards.Machine == "" || firstFacts.Guards.Machine != secondFacts.Guards.Machine || firstFacts.Guards.RuntimeConfig == "" || firstFacts.Guards.RuntimeConfig == secondFacts.Guards.RuntimeConfig {
		t.Fatalf("runtime binding lost measurement guards: first=%+v, second=%+v", firstFacts.Guards, secondFacts.Guards)
	}
	if firstFacts.PurityAssertion == "" || firstFacts.PurityAssertion != secondFacts.PurityAssertion {
		t.Fatalf("runtime binding changed purity: first=%q, second=%q", firstFacts.PurityAssertion, secondFacts.PurityAssertion)
	}
	binary, built := fixtureArtifact(t, ctx, snapshot, subject.Package)
	var produced [2]gofresh.Fingerprint
	for i, binding := range []*gofresh.RuntimeBinding{first, second} {
		unit, err := binding.BeginIdentity(subject)
		if err != nil {
			t.Fatal(err)
		}
		defer unit.Abort()
		process, err := unit.Register(ctx, built, gofresh.ProcessDescription{Identity: []string{"first", "second"}[i], Capture: filepath.Join(t.TempDir(), "testlog")})
		if err != nil {
			t.Fatal(err)
		}
		wantEnv, err := []testEnvironmentValue{firstEnv, secondEnv}[i].For(pkgDir)
		if err != nil {
			t.Fatal(err)
		}
		if process.Directory() != pkgDir || !slices.Equal(process.Environment(), wantEnv) {
			t.Fatalf("execution %d used another binding: directory=%q environment=%v", i, process.Directory(), process.Environment())
		}
		executeFixtureContribution(t, ctx, process, binary, subject.Symbol)
		if err := unit.Submit(process, gofresh.Terminal{Kind: gofresh.NormalCompletion}); err != nil {
			t.Fatal(err)
		}
		final, err := unit.Close(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if final.Observation != gofresh.IdentityObservation || final.Verdict.Status != gofresh.Valid || final.Fingerprint.PurityAssertion != firstFacts.PurityAssertion {
			t.Fatalf("purity changed identity-only support: %+v", final)
		}
		produced[i] = final.Fingerprint
		inputs, err := runtimeinput.Describe(final.Fingerprint.RuntimeInputs, root)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(inputs.EnvNames, "PEW_BINDING_VALUE") || !slices.Contains(inputs.EnvNames, "PWD") || !slices.Contains(inputs.Paths, filepath.Join(pkgDir, "input.txt")) {
			t.Fatalf("execution %d lost actual runtime inputs: %+v", i, inputs)
		}
		if v, err := binding.Check(ctx, subject, final.Fingerprint); err != nil || v.Status != gofresh.Valid {
			t.Fatalf("own runtime evidence refused: %+v, %v", v, err)
		}
	}
	if produced[0].RuntimeDigest == produced[1].RuntimeDigest {
		t.Fatal("distinct observed runtime values acquired one digest")
	}
	if v, err := second.Check(ctx, subject, produced[0]); err != nil || v.Status != gofresh.Stale {
		t.Fatalf("foreign runtime evidence served: %+v, %v", v, err)
	}
	// Equal measurement configuration cannot hide a moved observed value, even
	// under the explicit purity assertion. This check must reach the input guard.
	movedInput, err := snapshot.BindRuntime(ctx, gofresh.RuntimeDescription{Directory: pkgDir, Environment: secondEnv.WithParallelism(1).Values()})
	if err != nil {
		t.Fatal(err)
	}
	if v, err := movedInput.Check(ctx, subject, produced[0]); err != nil || v.Status != gofresh.Stale || !strings.Contains(v.Reason, "PEW_BINDING_VALUE") {
		t.Fatalf("observed runtime value escaped its binding: %+v, %v", v, err)
	}
	after, err := first.CurrentFacts(ctx, subject)
	if err != nil || after != firstFacts {
		t.Fatalf("second binding changed first facts: %+v, %v", after, err)
	}
	code, err := session.bind(ctx, []gofresh.Subject{subject}, gofresh.CodeResult)
	if err != nil {
		t.Fatal(err)
	}
	codeFacts, err := code.CurrentFacts(ctx, subject)
	if err != nil || codeFacts.Guards.Machine != "" || codeFacts.Guards.RuntimeConfig != "" || codeFacts.PurityAssertion != firstFacts.PurityAssertion {
		t.Fatalf("code facts gained measurement guards or lost purity: %+v, %v", codeFacts, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := snapshot.BindRuntime(cancelled, gofresh.RuntimeDescription{Directory: pkgDir, Environment: secondEnv.Values()}); !errors.Is(err, context.Canceled) {
		t.Fatalf("runtime binding lost caller cancellation: %v", err)
	}
}
