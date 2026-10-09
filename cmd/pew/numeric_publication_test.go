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

func TestRunInvalidSampleKeepsPriorRecordingAndSibling(t *testing.T) {
	if testing.Short() {
		t.Skip("loads a fixture package through the toolchain")
	}
	for _, bad := range []string{"NaN", "+Inf", "-Inf", "-1"} {
		t.Run(bad, func(t *testing.T) {
			dir := twoArmFixture(t)
			withWorkingDir(t, dir)
			st := store.New(filepath.Join(dir, "benchmarks"))
			writeStatRecording(t, st, "", "BenchmarkFirst", 42)
			path, err := st.Path("", "BenchmarkFirst", "")
			if err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			stub := armStub(nil)
			rc := runConfig{benchDir: st.Root, all: true, opts: run.Options{Count: 1, Benchtime: "1x", Bench: "."}, execute: func(dir, pin string, env, args []string) ([]byte, error) {
				out, err := stub(dir, pin, env, args)
				if bytes.Contains(out, []byte("BenchmarkFirst")) {
					out = bytes.ReplaceAll(out, []byte("5 ns/op"), []byte(bad+" ns/op"))
				}
				return out, err
			}}
			var out, errOut bytes.Buffer
			if err := runRun(context.Background(), &out, &errOut, rc, []string{"."}); err == nil {
				t.Fatalf("invalid run succeeded: %s", out.String())
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(before, after) {
				t.Fatal("invalid sample replaced prior recording")
			}
			if _, err := st.Read("", "BenchmarkSecond", ""); err != nil {
				t.Fatalf("lost good sibling: %v\n%s\n%s", err, out.String(), errOut.String())
			}
			if !strings.Contains(out.String(), "invalid") && !strings.Contains(errOut.String(), "invalid") {
				t.Fatalf("refusal not diagnosed: %s %s", out.String(), errOut.String())
			}
		})
	}
}
