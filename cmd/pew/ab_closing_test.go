package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	git "github.com/go-git/go-git/v5"
	"github.com/greatliontech/pew/internal/run"
)

func replaceFixtureExecutable(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".replacement", append(data, []byte("changed artifact")...), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path+".replacement", path); err != nil {
		t.Fatal(err)
	}
}

func TestABSuccessfulPairOwnsItsClosingDisposition(t *testing.T) {
	if testing.Short() {
		t.Skip("executes real pair completion and closing failures")
	}
	for _, mode := range []string{"cancel-completed", "expiry", "cancel-expiry", "changed-artifact", "changed-side-A", "before-side-B"} {
		t.Run(mode, func(t *testing.T) {
			root := abFixtureRepo(t)
			withWorkingDir(t, root)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			path := filepath.Join(t.TempDir(), "ab.txt")
			writeFile(t, path, "prior artifact\n")
			ac := abConfig{bench: "^BenchmarkWork$", count: 1, benchtime: "1x", ref: "HEAD", out: path}
			if strings.Contains(mode, "expiry") {
				ac.closingBound = time.Nanosecond
			}
			calls := 0
			ac.execute = func(dir, pin string, values []string, binary string, args []string) ([]byte, error) {
				calls++
				data, err := run.ExecuteBinaryContext(ctx, dir, pin, testEnvironment(t, values), binary, args)
				if err != nil {
					return data, err
				}
				if calls == 1 && mode == "before-side-B" {
					replaceFixtureExecutable(t, filepath.Join(filepath.Dir(binary), "b.test"))
				}
				if calls == 1 && mode == "changed-side-A" {
					replaceFixtureExecutable(t, binary)
				}
				if calls == 2 {
					if strings.HasPrefix(mode, "cancel-") {
						cancel()
					}
					if mode == "changed-artifact" {
						replaceFixtureExecutable(t, binary)
					}
				}
				return data, nil
			}
			err := runAB(ctx, io.Discard, io.Discard, ac, []string{"./p"})
			data, readErr := os.ReadFile(path)
			if readErr != nil || err == nil {
				t.Fatalf("mode=%s result=%v read=%v", mode, err, readErr)
			}
			if mode == "cancel-completed" {
				if exitCode(err) != 130 || strings.Count(string(data), "pew-ab-section: measurement") != 2 {
					t.Fatalf("completed pair lost on signal: %v\n%s", err, data)
				}
			} else if string(data) != "prior artifact\n" {
				t.Fatalf("refused closing replaced prior artifact: %v\n%s", err, data)
			}
			if strings.Contains(mode, "expiry") && !strings.Contains(err.Error(), "pair closing gate exceeded 1ns") {
				t.Fatalf("closing expiry unnamed: %v", err)
			}
			if mode == "cancel-expiry" && (exitCode(err) != 130 || !strings.Contains(err.Error(), "measured but not published")) {
				t.Fatalf("signal erased closing failure: %v", err)
			}
			if (strings.HasPrefix(mode, "changed-") || mode == "before-side-B") && !strings.Contains(err.Error(), "retained test executable changed") {
				t.Fatalf("artifact movement hidden: %v", err)
			}
			if mode == "before-side-B" && calls != 1 {
				t.Fatalf("changed side B reached executor: %d calls", calls)
			}
		})
	}
}

func TestABChecksSideABeforeTheFirstExecution(t *testing.T) {
	if testing.Short() {
		t.Skip("prepares real retained executables")
	}
	root := abFixtureRepo(t)
	env := testEnvironment(t, nil)
	p, err := abPackageMetadata(t.Context(), filepath.Join(root, "p"), env)
	if err != nil {
		t.Fatal(err)
	}
	ac := abConfig{bench: "^BenchmarkWork$", count: 1, benchtime: "1x", ref: "HEAD"}
	prep, err := prepareABPackage(t.Context(), ac, p, root, root, filepath.Dir(root), env)
	if prep != nil {
		t.Cleanup(func() { _ = os.RemoveAll(prep.tmp) })
	}
	if err != nil {
		t.Fatal(err)
	}
	replaceFixtureExecutable(t, prep.executables[0].path)
	entered := false
	ac.execute = func(string, string, []string, string, []string) ([]byte, error) {
		entered = true
		return nil, nil
	}
	err = abPackage(t.Context(), io.Discard, io.Discard, ac, prep, 1, 1, newEnvironments(env, ac.pin))
	if err == nil || !strings.Contains(err.Error(), "retained test executable changed") || entered {
		t.Fatalf("changed first artifact reached execution: entered=%t error=%v", entered, err)
	}
}

func TestABZeroExitDoesNotSupplyHarnessCompletion(t *testing.T) {
	if testing.Short() {
		t.Skip("executes a test binary which skips its harness")
	}
	for _, side := range []string{"A", "B"} {
		t.Run(side, func(t *testing.T) {
			root := abFixtureRepo(t)
			file := filepath.Join(root, "p", "p_test.go")
			original, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}
			writeFile(t, file, "package p\nimport (\"fmt\"; \"testing\")\nfunc BenchmarkWork(b *testing.B) {}\nfunc TestMain(m *testing.M) { fmt.Println(\"BenchmarkWork-2 1 5 ns/op\") }\n")
			if side == "B" {
				repo, err := git.PlainOpen(root)
				if err != nil {
					t.Fatal(err)
				}
				commitAll(t, repo, "uncompleted reference harness")
				writeFile(t, file, string(original))
			}
			withWorkingDir(t, root)
			path := filepath.Join(t.TempDir(), "ab.txt")
			writeFile(t, path, "prior artifact\n")
			err = runAB(t.Context(), io.Discard, io.Discard, abConfig{bench: "^BenchmarkWork$", count: 1, benchtime: "1x", ref: "HEAD", out: path}, []string{"./p"})
			data, readErr := os.ReadFile(path)
			if err == nil || !strings.Contains(err.Error(), "side "+side) || !strings.Contains(err.Error(), "benchmark harness completion missing") || readErr != nil || !bytes.Equal(data, []byte("prior artifact\n")) {
				t.Fatalf("zero exit became a completed pair: %v\n%s", err, data)
			}
		})
	}
}

type countedPairContext struct {
	context.Context
	cancel        context.CancelFunc
	after, checks int
}

func (c *countedPairContext) Err() error {
	c.checks++
	if c.checks >= c.after {
		c.cancel()
	}
	return c.Context.Err()
}

func TestABNonretainedPairHonorsCancellationDuringChildValidation(t *testing.T) {
	var raw strings.Builder
	for i := 0; i < 64; i++ {
		fmt.Fprintf(&raw, "BenchmarkWork/child%d-2 1 1 ns/op\n", i)
	}
	stream := fixtureABStream(t, []byte(raw.String()), "example.com/p", "BenchmarkWork")
	// The later cut is after entry and both 64-row walks: only the
	// successful nonretained exit remains to consult the context.
	for _, after := range []int{4, 2*64 + 2} {
		base, cancel := context.WithCancel(t.Context())
		defer cancel()
		ctx := &countedPairContext{Context: base, cancel: cancel, after: after}
		a := &abArtifact{}
		if err := a.pairContext(ctx, "example.com/p", "HEAD", 1, stream, stream); !errors.Is(err, context.Canceled) {
			t.Fatalf("child validation outlived its owning context: %v (%d checks, cut %d)", err, ctx.checks, after)
		}
	}
}

type manualPairDeadline struct{ context.Context }

func (c manualPairDeadline) Err() error {
	if c.Context.Err() != nil {
		return context.DeadlineExceeded
	}
	return nil
}

func TestABPairPublicationUsesItsExistingClosingGate(t *testing.T) {
	if testing.Short() {
		t.Skip("executes real pairs with deterministic publication expiry")
	}
	for _, stage := range []string{"staged", "committed"} {
		t.Run(stage, func(t *testing.T) {
			root := abFixtureRepo(t)
			env := testEnvironment(t, nil)
			p, err := abPackageMetadata(t.Context(), filepath.Join(root, "p"), env)
			if err != nil {
				t.Fatal(err)
			}
			var expire context.CancelFunc
			ac := abConfig{bench: "^BenchmarkWork$", count: 1, benchtime: "1x", ref: "HEAD", closingContext: func(parent context.Context, bound time.Duration) (context.Context, context.CancelFunc) {
				if bound != armWriteGateBound {
					t.Fatalf("closing bound=%s", bound)
				}
				ctx, cancel := context.WithCancel(context.WithoutCancel(parent))
				expire = cancel
				return manualPairDeadline{ctx}, cancel
			}}
			prep, err := prepareABPackage(t.Context(), ac, p, root, root, filepath.Dir(root), env)
			if prep != nil {
				t.Cleanup(func() { _ = os.RemoveAll(prep.tmp) })
			}
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "ab.txt")
			writeFile(t, path, "prior artifact\n")
			seen := false
			a := &abArtifact{path: path, observeWrite: func(got string) {
				if got == stage {
					seen = true
					expire()
				}
			}}
			ac.artifact = a
			var streams [2][]byte
			calls := 0
			ac.execute = func(dir, pin string, values []string, binary string, args []string) ([]byte, error) {
				data, err := run.ExecuteBinaryContext(t.Context(), dir, pin, testEnvironment(t, values), binary, args)
				streams[calls] = data
				calls++
				return data, err
			}
			err = abPackage(t.Context(), io.Discard, io.Discard, ac, prep, 1, 1, newEnvironments(env, ac.pin))
			data, readErr := os.ReadFile(path)
			if !seen || readErr != nil {
				t.Fatalf("publication stage not reached: %v %v", err, readErr)
			}
			if stage == "committed" {
				if err != nil || len(a.blocks) != 2 || strings.Count(string(data), "pew-ab-section: measurement") != 2 {
					t.Fatalf("committed pair reported lost: %v\n%s", err, data)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), "pair closing gate exceeded") || string(data) != "prior artifact\n" || len(a.blocks) != 0 {
				t.Fatalf("expired gate escaped or queued failed pair: blocks=%d error=%v\n%s", len(a.blocks), err, data)
			}
			a.observeWrite = nil
			if err := a.pairContext(t.Context(), p.ImportPath, "HEAD", 2, fixtureABStream(t, streams[0], p.ImportPath, "BenchmarkWork"), fixtureABStream(t, streams[1], p.ImportPath, "BenchmarkWork")); err != nil {
				t.Fatal(err)
			}
			data, err = os.ReadFile(path)
			if err != nil || strings.Contains(string(data), "pew-ab-pair: 1\n") || strings.Count(string(data), "pew-ab-pair: 2\n") != 2 {
				t.Fatalf("later write published refused pair: %v\n%s", err, data)
			}
		})
	}
}
