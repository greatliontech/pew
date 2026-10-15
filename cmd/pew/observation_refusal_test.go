package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/greatliontech/gofresh"
)

func TestArmObservationNeverBorrowsMissingPremises(t *testing.T) {
	if testing.Short() {
		t.Skip("executes producing units")
	}
	if runtime.GOOS != "linux" {
		t.Skip("immutable-environment method is Linux-audited")
	}
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/premises\n\ngo 1.26\n")
	writeFile(t, filepath.Join(root, "bench_test.go"), `package premises
import ("os"; "testing")
func BenchmarkEnv(b *testing.B) { _ = os.Getenv("PEW_PREMISE") }
`)
	t.Setenv("PEW_PREMISE", "original")
	ctx := t.Context()
	env := testEnvironment(t, os.Environ())
	session, _, err := newEngineAt(ctx, root, root, false, env)
	if err != nil {
		t.Fatal(err)
	}
	subject := gofresh.Subject{Package: "example.com/premises", Symbol: "BenchmarkEnv"}
	parent, err := session.bind(ctx, []gofresh.Subject{subject}, gofresh.Measurement)
	if err != nil {
		t.Fatal(err)
	}
	binary, built := fixtureArtifact(t, ctx, parent.Snapshot(), subject.Package)
	for _, name := range []string{"complete", "identity-only", "missing log", "missing header", "contradictory operation", "foreign frame", "foreign environment", "foreign process"} {
		t.Run(name, func(t *testing.T) {
			arm, err := parent.Begin(subject)
			if name == "identity-only" {
				arm, err = parent.BeginIdentity(subject)
			}
			if err != nil {
				t.Fatal(err)
			}
			defer arm.Abort()
			if name == "foreign environment" {
				otherEnv := testEnvironment(t, append(env.Values(), "PEW_DIFFERENT_PROCESS=1"))
				otherSession, _, err := newEngineAtProducer(ctx, root, root, false, env, &otherEnv)
				if err != nil {
					t.Fatal(err)
				}
				otherSnapshot, err := otherSession.bind(ctx, []gofresh.Subject{subject}, gofresh.Measurement)
				if err != nil {
					t.Fatal(err)
				}
				other, err := otherSnapshot.Begin(subject)
				if err != nil {
					t.Fatal(err)
				}
				defer other.Abort()
				if _, err := other.Register(ctx, built, gofresh.ProcessDescription{Identity: "other-environment"}); err == nil {
					t.Fatal("artifact binding crossed source/build generations")
				}
				return
			}
			path := filepath.Join(t.TempDir(), "capture")
			process, err := arm.Register(ctx, built, gofresh.ProcessDescription{Identity: name, Capture: path})
			if err != nil {
				t.Fatal(err)
			}
			if name == "foreign frame" || name == "foreign process" {
				other, err := parent.Begin(subject)
				if err != nil {
					t.Fatal(err)
				}
				defer other.Abort()
				if err := other.Submit(process, gofresh.Terminal{Kind: gofresh.NormalCompletion}); err == nil {
					t.Fatal("contribution crossed unit/process ownership")
				}
				return
			}
			executeFixtureContribution(t, ctx, process, binary, subject.Symbol)
			switch name {
			case "missing log":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "missing header":
				writeFile(t, path, "getenv PEW_PREMISE\n")
			case "contradictory operation":
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				writeFile(t, path, string(data)+"open missing.txt\n")
			}
			if err := arm.Submit(process, gofresh.Terminal{Kind: gofresh.NormalCompletion}); err != nil {
				t.Fatal(err)
			}
			final, err := arm.Close(ctx)
			if err != nil {
				t.Fatal(err)
			}
			view, err := session.bind(context.Background(), []gofresh.Subject{subject}, gofresh.Measurement)
			if err != nil {
				t.Fatal(err)
			}
			verdict, err := view.Check(ctx, subject, final.Fingerprint)
			if err != nil {
				t.Fatal(err)
			}
			if (verdict.Status == gofresh.Valid) != (name == "complete") {
				t.Fatalf("%s: %+v", name, verdict)
			}
		})
	}
}
