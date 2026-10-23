package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh"
	"github.com/greatliontech/pew/internal/run"
	"github.com/greatliontech/pew/internal/store"
)

func TestRunRegeneratesHistoricalRuntimeEvidence(t *testing.T) {
	if testing.Short() {
		t.Skip("records, checks and regenerates a benchmark")
	}
	dir := twoArmFixture(t)
	withWorkingDir(t, dir)
	st := store.New(filepath.Join(dir, "benchmarks"))
	rc := runConfig{benchDir: st.Root, opts: run.Options{Count: 1, Benchtime: "1x", Bench: "^BenchmarkFirst$"}}
	if err := runRun(t.Context(), io.Discard, io.Discard, rc, []string{"."}); err != nil {
		t.Fatal(err)
	}
	rows, err := st.Read("", "BenchmarkFirst", "")
	if err != nil {
		t.Fatal(err)
	}
	a := admitRecording(rows, true)
	if !a.ok {
		t.Fatalf("admission: %+v", a)
	}
	current := a.fp.RuntimeInputs
	raw, err := base64.RawURLEncoding.DecodeString(current)
	if err != nil || !bytes.HasPrefix(raw, []byte(`{"v":4`)) {
		t.Fatalf("producer did not use generation4: %s, %v", raw, err)
	}
	executions := 0
	rc.binary = func(ctx context.Context, dir, pin string, env testEnvironmentValue, bin string, args []string) ([]byte, error) {
		executions++
		return run.ExecuteBinaryContext(ctx, dir, pin, env, bin, args)
	}
	if err := runRun(t.Context(), io.Discard, io.Discard, rc, []string{"."}); err != nil || executions != 0 {
		t.Fatalf("current control not served: executions=%d error=%v", executions, err)
	}
	old := a.fp
	old.RuntimeInputs = base64.RawURLEncoding.EncodeToString([]byte(`{"v":2}`))
	digest := sha256.Sum256([]byte("version 2\n"))
	old.RuntimeDigest = hex.EncodeToString(digest[:])[:32]
	old.ObservationAssertion, old.ObservationProof = "", gofresh.ObservationProof{}
	configs, err := run.FingerprintConfigs(old, rows[0].GetConfig(run.KeyTestVariantLedger.Name))
	if err != nil {
		t.Fatal(err)
	}
	for _, cfg := range configs {
		for _, row := range rows {
			row.SetConfig(cfg.Key, string(cfg.Value))
			index, _ := row.ConfigIndex(cfg.Key)
			row.Config[index].File = cfg.File
		}
	}
	if err := st.Write("", "BenchmarkFirst", "", rows); err != nil {
		t.Fatal(err)
	}
	planted, err := st.Read("", "BenchmarkFirst", "")
	if err != nil || admitRecording(planted, true).fp != old {
		t.Fatalf("historical fixture did not land: admission=%+v want=%+v err=%v", admitRecording(planted, true), old, err)
	}
	var status bytes.Buffer
	if err := runStatus(t.Context(), &status, st.Root, "", false, false, true, []string{"."}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(status.String(), `"benchmark":"BenchmarkFirst","verdict":"stale"`) {
		t.Fatalf("history was not explicitly stale: %s", &status)
	}
	back, err := st.Read("", "BenchmarkFirst", "")
	if err != nil || admitRecording(back, true).fp != old {
		t.Fatalf("inspection rewrote history: %v", err)
	}
	if err := runRun(t.Context(), io.Discard, io.Discard, rc, []string{"."}); err != nil || executions != 1 {
		t.Fatalf("history served or lost: executions=%d error=%v", executions, err)
	}
	back, err = st.Read("", "BenchmarkFirst", "")
	if err != nil {
		t.Fatal(err)
	}
	if got := admitRecording(back, true); !got.ok || got.fp.RuntimeInputs != current {
		t.Fatalf("remeasurement did not restore native evidence: %+v", got)
	}
}
