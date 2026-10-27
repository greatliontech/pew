package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/greatliontech/pew/internal/run"
	"github.com/greatliontech/pew/internal/store"
)

type afterRecording struct {
	bytes.Buffer
	action func()
}

func (w *afterRecording) Write(p []byte) (int, error) {
	if w.action != nil && bytes.Contains(p, []byte("recorded     ")) {
		f := w.action
		w.action = nil
		f()
	}
	return w.Buffer.Write(p)
}

func TestRetainedExecutableIsCheckedAtEachArmAndClosing(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("builds and executes a retained benchmark artifact")
	}
	for _, phase := range []string{"after execution", "between arms"} {
		t.Run(phase, func(t *testing.T) {
			dir := twoArmFixture(t)
			ctx := withWorkingDir(t, dir)
			st := store.New(filepath.Join(t.TempDir(), "recordings"))
			writeStatRecording(t, st, "", "BenchmarkFirst", 41)
			writeStatRecording(t, st, "", "BenchmarkSecond", 42)
			before := map[string][]byte{}
			for _, bench := range []string{"BenchmarkFirst", "BenchmarkSecond"} {
				data, err := st.ReadBytes(store.Key{Bench: bench})
				if err != nil {
					t.Fatal(err)
				}
				before[bench] = data
			}
			var path string
			change := func() {
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				replacement := path + ".replacement"
				if err := os.WriteFile(replacement, append(data, []byte("changed-after-build")...), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(replacement, path); err != nil {
					t.Fatalf("artifact replacement: %v", err)
				}
			}
			calls := 0
			rc := runConfig{all: true, benchDir: st.Root, opts: run.Options{Count: 1, Benchtime: "1x", Bench: "."}, binary: func(ctx context.Context, dir, pin string, env testEnvironmentValue, binary string, args []string) ([]byte, error) {
				calls++
				path = binary
				data, err := run.ExecuteBinaryContext(ctx, dir, pin, env, binary, args)
				if err == nil && phase == "after execution" && calls == 1 {
					change()
				}
				return data, err
			}}
			var out afterRecording
			if phase == "between arms" {
				out.action = change
			}
			err := runRun(ctx, &out, &bytes.Buffer{}, rc, []string{"."})
			if err == nil || !strings.Contains(out.String(), "retained test executable changed") || calls != 1 {
				t.Fatalf("artifact replacement crossed execution/closing: calls=%d, %v\n%s", calls, err, out.String())
			}
			for bench, prior := range before {
				data, err := st.ReadBytes(store.Key{Bench: bench})
				shouldChange := phase == "between arms" && bench == "BenchmarkFirst"
				if err != nil || bytes.Equal(data, prior) == shouldChange {
					t.Fatalf("%s persisted=%t, error=%v", bench, !bytes.Equal(data, prior), err)
				}
			}
		})
	}
}
