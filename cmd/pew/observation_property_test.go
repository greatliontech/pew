package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"testing/quick"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/runtimeinput"
	"github.com/greatliontech/pew/internal/gotool"
	runpkg "github.com/greatliontech/pew/internal/run"
)

// TestObservationPremisesProperty quantifies over log multiplicity/order and
// environment order, then checks every premise independently for each draw.
// The only supported operation class is immutable getenv. File operations are
// contradictions, never fabricated examples of supported file outcomes.
func TestObservationPremisesProperty(t *testing.T) {
	if testing.Short() {
		t.Skip("prepares native subject-bound outcome capabilities")
	}
	if runtime.GOOS != "linux" {
		t.Skip("immutable-environment support is Linux-audited")
	}
	ctx := t.Context()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/observationproperty\n\ngo 1.26\n")
	writeFile(t, filepath.Join(root, "bench_test.go"), `package observationproperty
import ("os"; "testing")
func BenchmarkEnv(b *testing.B) { _ = os.Getenv("PEW_PROPERTY_A"); _ = os.Getenv("PEW_PROPERTY_B") }
func BenchmarkOther(b *testing.B) { _ = os.Getenv("PEW_PROPERTY_A") }
`)
	t.Setenv("PEW_PROPERTY_A", "first")
	t.Setenv("PEW_PROPERTY_B", "second")
	env := testEnvironment(t, os.Environ())
	e, _, err := newEngineAt(ctx, root, root, false, env)
	if err != nil {
		t.Fatal(err)
	}
	subject := gofresh.Subject{Package: "example.com/observationproperty", Symbol: "BenchmarkEnv"}
	other := gofresh.Subject{Package: subject.Package, Symbol: "BenchmarkOther"}
	view, err := e.NewViewFor(ctx, []gofresh.Subject{subject, other}, root, gofresh.Measurement)
	if err != nil {
		t.Fatal(err)
	}
	arm, err := view.Sibling([]gofresh.Subject{subject})
	if err != nil {
		t.Fatal(err)
	}
	frame := runpkg.CaptureObservationFrame(ctx, root, "")
	const process = "timed:environment"
	frame.Outcome, err = arm.PrepareOutcomeSupport(ctx, frame.ProducerFrame, process)
	if err != nil {
		t.Fatal(err)
	}
	producerEnv, err := env.For(root)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := frame.OutcomeBinding(process, producerEnv)
	if err != nil || frame.Outcome.Reason(binding) != "" || len(frame.Outcome.Subjects()) != 1 {
		t.Fatalf("native positive capability unavailable: %v, %s", err, frame.Outcome.Reason(binding))
	}
	otherFP, err := view.CaptureObserved(ctx, other)
	if err != nil {
		t.Fatal(err)
	}
	foreign := runpkg.CaptureObservationFrame(ctx, root, "")
	logs := t.TempDir() // captures must not move the bracketed source tree
	roots := &runtimeinput.Roots{}
	var failure string
	check := func(seed uint64, multiplicity uint8) bool {
		r := rand.New(rand.NewSource(int64(seed)))
		entries := slices.Clone(producerEnv)
		r.Shuffle(len(entries), func(i, j int) { entries[i], entries[j] = entries[j], entries[i] })
		ops := []string{"getenv PEW_PROPERTY_A", "getenv PEW_PROPERTY_B"}
		for i := 0; i < int(multiplicity%16); i++ {
			ops = append(ops, ops[r.Intn(2)])
		}
		r.Shuffle(len(ops), func(i, j int) { ops[i], ops[j] = ops[j], ops[i] })
		logText := "# test log\n" + strings.Join(ops, "\n") + "\n"
		for _, mode := range []string{"valid", "failed-normal", "identity-only", "profile", "missing-support", "foreign-process", "foreign-frame", "foreign-env", "drop-env", "duplicate-first", "duplicate-last", "missing-completion", "foreign-completion", "abnormal", "missing-log", "headerless", "wrong-class", "cancelled"} {
			f, identity, values := frame, process, slices.Clone(entries)
			path := filepath.Join(logs, "capture")
			text := logText
			switch mode {
			case "identity-only", "missing-support":
				f.Outcome = runtimeinput.OutcomeSupport{}
			case "foreign-process":
				identity = fmt.Sprintf("diagnostic:%x", seed)
			case "foreign-frame":
				f.ProducerFrame = foreign.ProducerFrame
			case "foreign-env":
				values = slices.DeleteFunc(values, func(s string) bool { return strings.HasPrefix(s, "PEW_PROPERTY_A=") })
				values = append(values, fmt.Sprintf("PEW_PROPERTY_A=changed-%x", seed))
			case "duplicate-first":
				values = append([]string{"PEW_PROPERTY_A=shadow"}, values...)
			case "duplicate-last":
				values = append(values, "PEW_PROPERTY_A=shadow")
			case "drop-env":
				values = slices.DeleteFunc(values, func(s string) bool { return strings.HasPrefix(s, "PEW_PROPERTY_B=") })
			case "missing-log":
				path = filepath.Join(logs, "absent")
			case "headerless":
				text = strings.TrimPrefix(text, "# test log\n")
			case "wrong-class":
				text += "open missing-file\n"
			}
			// Both environment admission and receipt construction reject
			// ambiguous duplicate keys, independently of their order.
			if strings.HasPrefix(mode, "duplicate-") {
				_, envErr := gotool.NewEnvironment(values)
				_, receiptErr := f.Completion(identity, values, "")
				if envErr == nil || receiptErr == nil {
					failure = fmt.Sprintf("%s admitted ambiguous environment: %v, %v", mode, envErr, receiptErr)
					return false
				}
				continue
			}
			if mode != "missing-log" {
				writeFile(t, path, text)
			}
			callCtx, cancel := context.WithCancel(ctx)
			if mode == "cancelled" {
				cancel()
			}
			var observation runtimeinput.Observation
			var callErr error
			// The adapter supplies normal completion itself. Receipt absence,
			// abnormal termination and normally completed failing tests instead
			// exercise its underlying facade's terminal-judgment contract.
			direct := mode == "missing-completion" || mode == "foreign-completion" || mode == "abnormal" || mode == "failed-normal" || mode == "missing-support" || mode == "profile"
			if direct {
				reason := ""
				if mode == "abnormal" {
					reason = "process interrupted before flush"
				}
				receipt, err := f.Completion(identity, values, reason)
				if err != nil {
					t.Fatal(err)
				}
				if mode == "missing-completion" {
					receipt = runtimeinput.CompletionReceipt{}
				}
				if mode == "foreign-completion" {
					receipt, err = foreign.Completion(identity, values, "")
					if err != nil {
						t.Fatal(err)
					}
				}
				ingest := runtimeinput.ProducerIngest{Identity: identity, Env: values, Roots: roots, Completion: receipt, Outcome: f.Outcome}
				if mode == "profile" {
					// Profiling has an identity-only contract even if the
					// underlying subject otherwise has a native capability.
					observation, _, callErr = f.ObserveInputs(callCtx, path, ingest)
				} else {
					observation, _, callErr = f.Observe(callCtx, path, ingest)
				}
			} else {
				observation, callErr = runpkg.IngestObservation(callCtx, f, path, identity, roots, testEnvironment(t, values))
			}
			cancel()
			fail := func(format string, args ...any) bool {
				failure = fmt.Sprintf("mode=%s seed=%d: ", mode, seed) + fmt.Sprintf(format, args...)
				return false
			}
			if mode == "cancelled" {
				if !errors.Is(callErr, context.Canceled) || observation.Manifest != "" {
					return fail("cancellation produced evidence: %+v, %v", observation, callErr)
				}
				continue
			}
			if callErr != nil {
				return fail("ingest: %v", callErr)
			}
			// Oracle: only a flushed getenv capture with all matching premises
			// grants the prepared subject outcomes. Normal test failure is not
			// abnormal termination. Identity guards never grant outcome support.
			wantSupport := mode == "valid" || mode == "failed-normal"
			gotSupport := runtimeinput.HasOutcomeSupport(observation.Manifest, frame.Outcome.Subjects()[0])
			if gotSupport != wantSupport {
				return fail("support=%t want %t: %+v", gotSupport, wantSupport, observation)
			}
			incomplete := mode == "missing-completion" || mode == "foreign-completion" || mode == "abnormal" || mode == "missing-support" || mode == "missing-log" || mode == "headerless" || mode == "wrong-class"
			if observation.Unverifiable != incomplete || (incomplete && observation.Reason == "") {
				return fail("incomplete=%t want %t: %+v", observation.Unverifiable, incomplete, observation)
			}
			if _, err := runtimeinput.CompletedState(observation); err != nil {
				return fail("unsealed observation: %v", err)
			}
			if wantSupport || mode == "identity-only" || mode == "profile" {
				transaction, err := view.Sibling([]gofresh.Subject{subject})
				if err != nil {
					t.Fatal(err)
				}
				captured, err := transaction.CaptureObserved(ctx, subject)
				if err != nil {
					t.Fatal(err)
				}
				attached, err := transaction.AttachObservation(subject, captured, observation)
				if err != nil {
					return fail("attach: %v", err)
				}
				if err := transaction.Validate(ctx); err != nil {
					return fail("producer validation: %v", err)
				}
				verdict, err := view.CheckObserved(ctx, attached, subject)
				if err != nil || (verdict.Status == gofresh.Valid) != wantSupport {
					return fail("observed verdict=%+v error=%v", verdict, err)
				}
				borrowed := otherFP
				borrowed.RuntimeInputs, borrowed.RuntimeDigest = observation.Manifest, observation.Digest
				verdict, err = view.CheckObserved(ctx, borrowed, other)
				if err != nil || verdict.Status == gofresh.Valid {
					return fail("other subject borrowed outcomes: %+v, %v", verdict, err)
				}
			}
			// Exercise the sealed in-memory contributing-process identity, not
			// just the encoded outcome bit: conflicting evidence for this very
			// process must refuse, while another process remains mergeable.
			duplicate, err := runtimeinput.Merge(root, values, observation, observation)
			if err != nil || duplicate.Manifest != observation.Manifest || duplicate.Digest != observation.Digest {
				return fail("duplicate changed evidence: %+v, %v", duplicate, err)
			}
			conflict, err := runtimeinput.Incomplete(root, identity, "different terminal evidence", values)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := runtimeinput.Merge(root, values, observation, conflict); err == nil {
				return fail("conflicting same-process evidence merged")
			}
			separate, err := runtimeinput.Incomplete(root, identity+":separate", "different terminal evidence", values)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := runtimeinput.Merge(root, values, observation, separate); err != nil {
				return fail("distinct processes conflated: %v", err)
			}
			if mode == "identity-only" || mode == "profile" {
				data, err := base64.RawURLEncoding.DecodeString(observation.Manifest)
				var manifest struct {
					Outcome  string
					Subjects []string
					Env      []json.RawMessage
				}
				if err != nil || json.Unmarshal(data, &manifest) != nil || manifest.Outcome != "" || len(manifest.Subjects) != 0 || len(manifest.Env) != 2 {
					return fail("identity-only lost guards or gained outcomes: %s", data)
				}
			}
		}
		return true
	}
	if err := quick.Check(check, &quick.Config{MaxCount: 20, Rand: rand.New(rand.NewSource(731))}); err != nil {
		t.Fatalf("%s\n%v", failure, err)
	}
}
