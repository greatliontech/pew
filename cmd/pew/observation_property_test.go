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
)

// TestObservationPremisesProperty checks the owned producer protocol over
// generated capture order/multiplicity and environment order. These are protocol
// inputs, not claims that a synthetic transcript proves an OS exit. Separate
// real-process tests pin the executor's terminal judgment. Immutable getenv is
// the positive operation class; a file operation contradicts that inventory.
func TestObservationPremisesProperty(t *testing.T) {
	if testing.Short() {
		t.Skip("prepares subject-bound producer capabilities")
	}
	if runtime.GOOS != "linux" {
		t.Skip("immutable-environment support is Linux-audited")
	}
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/observationproperty\n\ngo 1.26\n")
	writeFile(t, filepath.Join(root, "bench_test.go"), `package observationproperty
import ("os"; "testing")
func BenchmarkEnv(b *testing.B) { _ = os.Getenv("PEW_PROPERTY_A"); _ = os.Getenv("PEW_PROPERTY_B") }
func BenchmarkOther(b *testing.B) { _ = os.Getenv("PEW_PROPERTY_A") }
`)
	t.Setenv("PEW_PROPERTY_A", "first")
	t.Setenv("PEW_PROPERTY_B", "second")
	ctx := t.Context()
	env := testEnvironment(t, os.Environ())
	session, _, err := newEngineAt(ctx, root, root, false, env)
	if err != nil {
		t.Fatal(err)
	}
	subject := gofresh.Subject{Package: "example.com/observationproperty", Symbol: "BenchmarkEnv"}
	other := gofresh.Subject{Package: subject.Package, Symbol: "BenchmarkOther"}
	parent, err := session.Snapshot(ctx, []gofresh.Subject{subject, other}, gofresh.Measurement)
	if err != nil {
		t.Fatal(err)
	}
	binary, built := fixtureArtifact(t, ctx, parent, subject.Package)
	// Establish the admitted positive control from the actual retained binary.
	positive, err := parent.Begin(subject)
	if err != nil {
		t.Fatal(err)
	}
	defer positive.Abort()
	process, err := positive.Register(ctx, built, gofresh.ProcessDescription{Identity: "real-positive", Capture: filepath.Join(t.TempDir(), "actual")})
	if err != nil {
		t.Fatal(err)
	}
	executeFixtureContribution(t, ctx, process, binary, subject.Symbol)
	if err := positive.Submit(process, gofresh.Terminal{Kind: gofresh.NormalCompletion}); err != nil {
		t.Fatal(err)
	}
	control, err := positive.Close(ctx)
	if err != nil || control.Observation != gofresh.SupportedObservation || control.Verdict.Status != gofresh.Valid {
		t.Fatalf("native positive control: %+v, %v", control, err)
	}
	var failure string
	check := func(seed uint64, multiplicity uint8) bool {
		r := rand.New(rand.NewSource(int64(seed)))
		ops := []string{"getenv PEW_PROPERTY_A", "getenv PEW_PROPERTY_B"}
		for i := 0; i < int(multiplicity%16); i++ {
			ops = append(ops, ops[r.Intn(2)])
		}
		r.Shuffle(len(ops), func(i, j int) { ops[i], ops[j] = ops[j], ops[i] })
		text := "# test log\n" + strings.Join(ops, "\n") + "\n"
		for _, mode := range []string{"valid", "failed-normal", "identity-only", "missing-terminal", "never-started", "abnormal", "missing-log", "headerless", "wrong-class", "cancelled", "aborted", "borrowed", "duplicate-terminal", "late-contribution"} {
			if !func() bool {
				fail := func(format string, args ...any) bool {
					failure = fmt.Sprintf("mode=%s seed=%d: ", mode, seed) + fmt.Sprintf(format, args...)
					return false
				}
				unit, err := parent.Begin(subject)
				if mode == "identity-only" {
					unit, err = parent.BeginIdentity(subject)
				}
				if err != nil {
					return fail("begin: %v", err)
				}
				defer unit.Abort()
				path := filepath.Join(t.TempDir(), "capture")
				c, err := unit.Register(ctx, built, gofresh.ProcessDescription{Identity: fmt.Sprintf("generated-%x-%s", seed, mode), Capture: path})
				if err != nil {
					return fail("register: %v", err)
				}
				// The environment is owned. Reordering or corrupting a returned
				// copy cannot change this process's admitted environment.
				original := c.Environment()
				copy := c.Environment()
				r.Shuffle(len(copy), func(i, j int) { copy[i], copy[j] = copy[j], copy[i] })
				copy[0] = "PEW_PROPERTY_A=foreign"
				if !slices.Equal(original, c.Environment()) {
					return fail("process environment borrowed mutable caller storage")
				}
				if mode == "borrowed" {
					sibling, err := parent.Begin(other)
					if err != nil {
						return fail("sibling: %v", err)
					}
					defer sibling.Abort()
					if err := sibling.Submit(c, gofresh.Terminal{Kind: gofresh.NormalCompletion}); err == nil {
						return fail("sibling borrowed a process/frame/subject terminal")
					}
					return true
				}
				capture := text
				if mode == "headerless" {
					capture = strings.TrimPrefix(capture, "# test log\n")
				}
				if mode == "wrong-class" {
					capture += "open missing-file\n"
				}
				if mode != "missing-log" {
					writeFile(t, path, capture)
				}
				terminal := gofresh.Terminal{Kind: gofresh.NormalCompletion}
				if mode == "abnormal" || mode == "never-started" {
					terminal = gofresh.Terminal{Kind: gofresh.AbnormalCompletion, Reason: "process interrupted before flush"}
					if mode == "never-started" {
						terminal.Kind = gofresh.NeverStarted
					}
				}
				if mode != "missing-terminal" {
					if err := unit.Submit(c, terminal); err != nil {
						return fail("terminal: %v", err)
					}
				}
				if mode == "duplicate-terminal" && unit.Submit(c, terminal) == nil {
					return fail("terminal recorded twice")
				}
				closing, cancel := context.WithCancel(ctx)
				defer cancel()
				if mode == "cancelled" {
					cancel()
				}
				if mode == "aborted" {
					unit.Abort()
				}
				final, err := unit.Close(closing)
				if mode == "missing-terminal" || mode == "never-started" || mode == "cancelled" || mode == "aborted" {
					if err == nil || final.Fingerprint.MaximalClosure != "" {
						return fail("unsettled unit minted evidence: %+v, %v", final, err)
					}
					if mode == "cancelled" && !errors.Is(err, context.Canceled) {
						return fail("lost caller cancellation: %v", err)
					}
					return true
				}
				if err != nil {
					return fail("close: %v", err)
				}
				wantSupport := mode == "valid" || mode == "failed-normal" || mode == "duplicate-terminal" || mode == "late-contribution"
				if (final.Observation == gofresh.SupportedObservation) != wantSupport || (final.Verdict.Status == gofresh.Valid) != wantSupport {
					return fail("support=%v verdict=%+v, want support=%t", final.Observation, final.Verdict, wantSupport)
				}
				if mode == "identity-only" {
					if final.Observation != gofresh.IdentityObservation || final.ObservationReason != "" {
						return fail("deliberate identity evidence became failed capture: %+v", final)
					}
				} else if !wantSupport && (final.Observation != gofresh.IncompleteObservation || final.Verdict.Status != gofresh.Unverifiable || final.ObservationReason == "") {
					return fail("incomplete capture lost its disposition or reason: %+v", final)
				}
				if mode == "late-contribution" {
					if _, err := unit.Register(ctx, built, gofresh.ProcessDescription{Identity: "late", Capture: path}); err == nil {
						return fail("closed roster admitted a contribution")
					}
				}
				data, err := base64.RawURLEncoding.DecodeString(final.Fingerprint.RuntimeInputs)
				var manifest struct {
					Outcome  string
					Subjects []string
					Env      []json.RawMessage
				}
				if err != nil || json.Unmarshal(data, &manifest) != nil {
					return fail("unreadable native manifest: %s", data)
				}
				if mode == "identity-only" && (manifest.Outcome != "" || len(manifest.Subjects) != 0 || len(manifest.Env) != 2) {
					return fail("identity-only lost guards or gained outcomes: %s", data)
				}
				return true
			}() {
				return false
			}
		}
		return true
	}
	if err := quick.Check(check, &quick.Config{MaxCount: 20, Rand: rand.New(rand.NewSource(731))}); err != nil {
		t.Fatalf("%s\n%v", failure, err)
	}
}
