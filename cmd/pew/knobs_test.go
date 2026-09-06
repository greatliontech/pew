package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/greatliontech/pew/internal/run"
	"github.com/greatliontech/pew/internal/store"
	"github.com/spf13/cobra"
)

// The knob surface is derived where the host decides the value (spec
// §12): --pin is a switch on run and ab, never a CPU list; ab has no
// allocation switch (-benchmem is always on, §9); the per-invocation
// purity flags are gone — the source directives are the one channel
// (§7.5).
func TestKnobSurfaceIsDerived(t *testing.T) {
	runFlags, abFlags := newRunCmd().Flags(), newABCmd().Flags()
	if f := runFlags.Lookup("pin"); f == nil || f.Value.Type() != "bool" {
		t.Errorf("run --pin = %v, want a bool switch", f)
	}
	if f := abFlags.Lookup("pin"); f == nil || f.Value.Type() != "bool" {
		t.Errorf("ab --pin = %v, want a bool switch", f)
	}
	for _, gone := range []string{"assume-pure", "impure"} {
		if runFlags.Lookup(gone) != nil {
			t.Errorf("run --%s still exists; the source directive is the one purity channel", gone)
		}
	}
	if abFlags.Lookup("benchmem") != nil {
		t.Error("ab --benchmem still exists; allocation statistics are always captured")
	}
}

// hostPinRefusal is the reason this host cannot serve a --pin, if any:
// no derivable set, or no taskset to apply it through.
func hostPinRefusal() error {
	if _, err := run.DerivePin(); err != nil {
		return err
	}
	_, err := exec.LookPath("taskset")
	return err
}

// A pin request reports the derived set and its derivation before the
// verb spends anything; a host the set cannot be derived on, or without
// taskset to apply it, refuses under the flag's name
// (REQ-pew-pin-derivation). Both outcomes are pinned so the test holds
// on a host with and without a readable topology, and the taskset leg
// is forced by emptying PATH.
func TestPinSwitchReportsItsDerivation(t *testing.T) {
	if _, err := run.DerivePin(); err == nil {
		t.Run("no taskset", func(t *testing.T) {
			t.Setenv("PATH", t.TempDir())
			var errw bytes.Buffer
			if _, err := derivePin(&errw); err == nil || !strings.Contains(err.Error(), "taskset") || errw.Len() != 0 {
				t.Fatalf("derivePin without taskset = %v (reported %q); want the refusal before any report", err, errw.String())
			}
		})
	}
	var errw bytes.Buffer
	pin, err := derivePin(&errw)
	want, _ := run.DerivePin()
	wantErr := hostPinRefusal()
	if wantErr != nil {
		if err == nil || !strings.HasPrefix(err.Error(), "--pin: ") || !strings.Contains(err.Error(), wantErr.Error()) {
			t.Fatalf("derivePin = %+v, %v; want the host's refusal %q under --pin", pin, err, wantErr)
		}
		if errw.Len() != 0 {
			t.Fatalf("a refused pin reported %q", errw.String())
		}
		return
	}
	if err != nil {
		t.Fatalf("derivePin: %v (host derives %s)", err, want.List())
	}
	if pin.List() != want.List() || !pin.Pinned() {
		t.Fatalf("derivePin = %+v, want the host's %+v", pin, want)
	}
	if got := errw.String(); got != fmt.Sprintf("pew: pinning to CPUs %s (%s)\n", want.List(), want.Reason) {
		t.Fatalf("report = %q", got)
	}
}

// ab always passes -test.benchmem to both sides (spec §9: allocation
// metrics ride every pew measurement).
func TestABAlwaysCapturesAllocations(t *testing.T) {
	dir := abFixtureRepo(t)
	prior, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(filepath.Join(dir, "p")); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(prior)
	var withBenchmem, runs int
	ac := abConfig{
		bench: ".", count: 1, ref: "HEAD",
		throttle: func() run.ThrottleSnapshot { return run.ThrottleSnapshot{} },
		build:    func(string, []string, []string) error { return nil },
		execute: func(execDir, pin string, env []string, bin string, args []string) ([]byte, error) {
			runs++
			for _, a := range args {
				if a == "-test.benchmem" {
					withBenchmem++
				}
			}
			return []byte("BenchmarkWork-8 1000 90 ns/op 8 B/op 1 allocs/op\n"), nil
		},
	}
	var out, errOut bytes.Buffer
	if err := runAB(context.Background(), &out, &errOut, ac, []string{"."}); err != nil {
		t.Fatalf("runAB: %v\nstderr: %s", err, errOut.String())
	}
	if runs != 2 || withBenchmem != 2 {
		t.Fatalf("runs = %d, with -test.benchmem = %d; want both sides with it", runs, withBenchmem)
	}
}

// The measured process's environment states the pin's width as
// GOMAXPROCS: an operator's own non-empty value is kept, an empty one
// is no configuration and is replaced, the width is the pin's CPU
// count bounded by this process's own default, and an unpinned
// environment is untouched.
func TestPinEnvironmentStatesTheWidth(t *testing.T) {
	two := run.Pin{CPUs: []int{3, 15}}
	for name, tc := range map[string]struct {
		env  []string
		pin  run.Pin
		want string
	}{
		"operator's value kept":      {[]string{"GOMAXPROCS=1", "X=y"}, two, "GOMAXPROCS=1 X=y"},
		"empty value replaced":       {[]string{"GOMAXPROCS=", "X=y"}, two, "X=y GOMAXPROCS=2"},
		"width stated":               {[]string{"X=y"}, two, "X=y GOMAXPROCS=2"},
		"unpinned untouched":         {[]string{"X=y"}, run.Pin{}, "X=y"},
		"bounded by the own default": {[]string{"X=y"}, run.Pin{CPUs: make([]int, runtime.GOMAXPROCS(0)+5)}, "X=y GOMAXPROCS=" + strconv.Itoa(runtime.GOMAXPROCS(0))},
	} {
		if got := strings.Join(pinEnvironment(tc.env, tc.pin), " "); got != tc.want {
			t.Errorf("%s: pinEnvironment = %q, want %q", name, got, tc.want)
		}
	}
	// An unpinned run declares no producer environment: the two
	// environments are one, and the measured one is the analysis one.
	if envs := newEnvironments([]string{"X=y"}, run.Pin{}); envs.runtime != nil || strings.Join(envs.measured(), " ") != "X=y" {
		t.Errorf("unpinned environments = %+v, want no runtime environment", envs)
	}
	if envs := newEnvironments([]string{"X=y"}, two); strings.Join(envs.runtime, " ") != "X=y GOMAXPROCS=2" || strings.Join(envs.analysis, " ") != "X=y" {
		t.Errorf("pinned environments = %+v, want the analysis one wide and the runtime one pinned", envs)
	}
}

// A pinned run threads the derived set to taskset and states the pin's
// width as GOMAXPROCS in the measured process's environment and the
// engine's producer environment only — the warm-up build and every
// other go child stay on the analysis environment — so the recording
// is stale under an unpinned environment: a one-core baseline never
// serves an unpinned run as valid (REQ-pew-pin-derivation).
func TestPinnedRunGuardsItsPin(t *testing.T) {
	if testing.Short() {
		t.Skip("loads a fixture package through the toolchain with the execute seam stubbed")
	}
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":      "module example.com/pinned\n\ngo 1.26.4\n",
		"a/a_test.go": "package a\n\nimport \"testing\"\n\nfunc BenchmarkA(b *testing.B) {}\n",
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	commitFixture(t, dir)
	withWorkingDir(t, dir)
	// An empty GOMAXPROCS in the operator's environment is no
	// configuration: the width is still stated.
	t.Setenv("GOMAXPROCS", "")
	benchDir := filepath.Join(dir, "benchmarks")
	pin := run.Pin{CPUs: []int{3, 15}}
	var pins, maxprocs, buildMaxprocs []string
	rc := runConfig{
		benchDir: benchDir, pin: pin,
		opts:     run.Options{Count: 1, Benchtime: "1x", Bench: "."},
		throttle: func() run.ThrottleSnapshot { return run.ThrottleSnapshot{"c0": 1} },
		execute: func(moduleDir, pin string, env, args []string) ([]byte, error) {
			for _, a := range args {
				if a == "-c" {
					for _, kv := range env {
						if strings.HasPrefix(kv, "GOMAXPROCS=") && kv != "GOMAXPROCS=" {
							buildMaxprocs = append(buildMaxprocs, kv)
						}
					}
					return nil, nil
				}
			}
			pins = append(pins, pin)
			for _, kv := range env {
				if strings.HasPrefix(kv, "GOMAXPROCS=") {
					maxprocs = append(maxprocs, kv)
				}
			}
			return []byte("goos: linux\ngoarch: amd64\npkg: example.com/pinned/a\ncpu: T\nBenchmarkA-2 1 5 ns/op\nPASS\n"), nil
		},
	}
	var out bytes.Buffer
	if err := runRun(context.Background(), &out, &bytes.Buffer{}, rc, []string{"./..."}); err != nil {
		t.Fatalf("runRun: %v\n%s", err, out.String())
	}
	if strings.Join(pins, " ") != "3,15" || strings.Join(maxprocs, " ") != "GOMAXPROCS=2" {
		t.Fatalf("measurement ran with pins %v and %v; want the derived set and its width", pins, maxprocs)
	}
	if len(buildMaxprocs) != 0 {
		t.Fatalf("the warm-up build ran under %v; builds are not pinned", buildMaxprocs)
	}
	// The unpinned environment reads the pinned recording stale on the
	// runtime-configuration guard.
	e, _, err := newEngineAt(dir, filepath.Join(dir, "a"), false, os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	v, reason, _, _, err := checkOne(store.New(benchDir), e, "example.com/pinned/a", "a", dir, "BenchmarkA", "")
	if err != nil {
		t.Fatalf("checkOne: %v", err)
	}
	if v != verdictStale || reason != "runtimeconfig" {
		t.Fatalf("unpinned verdict over the pinned recording = {%s %q}, want {stale runtimeconfig}", v, reason)
	}
	// The pinned environment reads it as its own.
	pinned, _, err := newEngineAtProducer(dir, filepath.Join(dir, "a"), false, os.Environ(), pinEnvironment(os.Environ(), pin))
	if err != nil {
		t.Fatal(err)
	}
	if v, reason, _, _, err = checkOne(store.New(benchDir), pinned, "example.com/pinned/a", "a", dir, "BenchmarkA", ""); err != nil || reason == "runtimeconfig" {
		t.Fatalf("pinned verdict over the pinned recording = {%s %q}, %v; want the guard to hold", v, reason, err)
	}
}

// A pin request is answered before the listing: with `--pin` and a
// package pattern nothing lists, the report (or the host's refusal)
// precedes the listing's failure (REQ-pew-pin-derivation,
// REQ-pew-preparation).
func TestPinIsDerivedBeforeTheListing(t *testing.T) {
	hostErr := hostPinRefusal()
	for _, verb := range []struct {
		name string
		cmd  func() *cobra.Command
	}{{"run", newRunCmd}, {"ab", newABCmd}} {
		t.Run(verb.name, func(t *testing.T) {
			cmd := verb.cmd()
			var out, errw bytes.Buffer
			cmd.SetArgs([]string{"--pin", "./definitely/not/a/package"})
			cmd.SetOut(&out)
			cmd.SetErr(&errw)
			err := cmd.Execute()
			if hostErr != nil {
				if err == nil || !strings.Contains(err.Error(), "--pin: ") {
					t.Fatalf("%s --pin on a host without a derivable pin = %v; want the pin refusal before the listing", verb.name, err)
				}
				return
			}
			if !strings.HasPrefix(errw.String(), "pew: pinning to CPUs ") {
				t.Fatalf("%s --pin did not report the pin first: stderr %q, err %v", verb.name, errw.String(), err)
			}
			if err == nil {
				t.Fatalf("%s listed a package that does not exist", verb.name)
			}
		})
	}
}

// A run inside a linked worktree (`git worktree add`) records exactly as
// in a plain clone: the repository state and the committed blobs resolve
// through the worktree's common directory (spec §6.1; the field failure
// was a whole-package "worktree status: object not found").
func TestRunRecordsFromALinkedWorktree(t *testing.T) {
	if testing.Short() {
		t.Skip("loads a fixture package through the toolchain with the execute seam stubbed")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git binary not available")
	}
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":      "module example.com/linked\n\ngo 1.26.4\n",
		"a/a_test.go": "package a\n\nimport \"testing\"\n\nfunc BenchmarkA(b *testing.B) {}\n",
	}
	for name, content := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	commitFixture(t, dir)
	linked := filepath.Join(t.TempDir(), "linked")
	if out, err := exec.Command("git", "-C", dir, "worktree", "add", "-q", "--detach", linked).CombinedOutput(); err != nil {
		t.Fatalf("git worktree add: %v: %s", err, out)
	}
	withWorkingDir(t, linked)
	rc := runConfig{
		benchDir: filepath.Join(linked, "benchmarks"),
		opts:     run.Options{Count: 1, Benchtime: "1x", Bench: "."},
		throttle: func() run.ThrottleSnapshot { return run.ThrottleSnapshot{"c0": 1} },
		execute: func(moduleDir, pin string, env, args []string) ([]byte, error) {
			for _, a := range args {
				if a == "-c" {
					return nil, nil
				}
			}
			return []byte("goos: linux\ngoarch: amd64\npkg: example.com/linked/a\ncpu: T\nBenchmarkA-2 1 5 ns/op\nPASS\n"), nil
		},
	}
	var out, errw bytes.Buffer
	if err := runRun(context.Background(), &out, &errw, rc, []string{"./..."}); err != nil {
		t.Fatalf("run in a linked worktree: %v\n%s%s", err, out.String(), errw.String())
	}
	if !strings.Contains(out.String(), "recorded     example.com/linked/a.BenchmarkA") || strings.Contains(out.String(), "object not found") {
		t.Fatalf("linked worktree run:\n%s", out.String())
	}
}
