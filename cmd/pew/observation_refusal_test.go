package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/runtimeinput"
	runpkg "github.com/greatliontech/pew/internal/run"
)

func TestArmObservationNeverBorrowsMissingPremises(t *testing.T) {
	if testing.Short() {
		t.Skip("prepares static outcome support")
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
	ctx := context.Background()
	env := testEnvironment(t, os.Environ())
	e, _, err := newEngineAt(ctx, root, root, false, env)
	if err != nil {
		t.Fatal(err)
	}
	subject := gofresh.Subject{Package: "example.com/premises", Symbol: "BenchmarkEnv"}
	parent, err := e.NewViewFor(ctx, []gofresh.Subject{subject}, root, gofresh.Measurement)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"complete", "identity-only", "missing log", "missing header", "contradictory operation", "foreign frame", "foreign environment", "foreign process"} {
		t.Run(name, func(t *testing.T) {
			arm, err := parent.Sibling([]gofresh.Subject{subject})
			if err != nil {
				t.Fatal(err)
			}
			frame := runpkg.CaptureObservationFrame(ctx, root, "")
			frame.Outcome, err = arm.PrepareOutcomeSupport(ctx, frame.ProducerFrame, "arm")
			if err != nil {
				t.Fatal(err)
			}
			fp, err := arm.CaptureObserved(ctx, subject)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "capture")
			log := "# test log\ngetenv PEW_PREMISE\n"
			identity := "arm"
			ingestEnv := env
			switch name {
			case "identity-only":
				frame.Outcome = runtimeinput.OutcomeSupport{}
			case "missing header":
				log = "getenv PEW_PREMISE\n"
			case "contradictory operation":
				log += "open missing.txt\n"
			case "foreign frame":
				frame.ProducerFrame = runtimeinput.CaptureProducerFrame(ctx, root, root, runtimeinput.FrameOptions{})
			case "foreign environment":
				ingestEnv = testEnvironment(t, append(env.Values(), "PEW_DIFFERENT_PROCESS=1"))
			case "foreign process":
				identity = "other-arm"
			}
			if name != "missing log" {
				writeFile(t, path, log)
			}
			observation, err := runpkg.IngestObservation(ctx, frame, path, identity, &runtimeinput.Roots{}, ingestEnv)
			if err != nil {
				t.Fatal(err)
			}
			attached, err := arm.AttachObservation(subject, fp, observation)
			if err != nil {
				t.Fatal(err)
			}
			if err := arm.Validate(ctx); err != nil {
				t.Fatal(err)
			}
			view, err := e.NewViewFor(ctx, []gofresh.Subject{subject}, root, gofresh.Measurement)
			if err != nil {
				t.Fatal(err)
			}
			verdict, err := view.CheckObserved(ctx, attached, subject)
			if err != nil {
				t.Fatal(err)
			}
			if err := view.Validate(ctx); err != nil {
				t.Fatal(err)
			}
			if (verdict.Status == gofresh.Valid) != (name == "complete") {
				t.Fatalf("%s: %+v", name, verdict)
			}
		})
	}
}
