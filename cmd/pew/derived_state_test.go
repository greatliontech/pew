package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gogit "github.com/go-git/go-git/v5"
	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/closure"
	"github.com/greatliontech/gofresh/runtimeinput"
	"github.com/greatliontech/pew/internal/recordingtest"
	"github.com/greatliontech/pew/internal/store"
)

// TestDerivedStateColdWarmDiscardedCacheAndDependencyEquivalence pins
// REQ-pew-derived-state across Pew's verdict path: cold computation, warm
// memoized hits, and discarded-cache recomputation produce identical semantic
// facts and verdicts; same-commit irrelevant edits preserve reuse while
// same-commit relevant source or selected local-dependency edits invalidate;
// distinct checkout coordinates do not alias; budget-cut availability never
// fabricates authority; and historical evidence lacking strategy metadata is
// never backfilled.
func TestDerivedStateColdWarmDiscardedCacheAndDependencyEquivalence(t *testing.T) {
	if testing.Short() {
		t.Skip("loads fixture packages through the toolchain and shared Gofresh session")
	}
	dirA := t.TempDir()
	writeFixture := func(root, depReturn, libBody, unusedBody string) {
		writeFile(t, filepath.Join(root, "go.mod"), "module example.com/derivedstate\n\ngo 1.26.4\n\nreplace example.com/dep => ./dep\n\nrequire example.com/dep v0.1.0\n")
		writeFile(t, filepath.Join(root, "dep", "go.mod"), "module example.com/dep\n\ngo 1.26.4\n")
		writeFile(t, filepath.Join(root, "dep", "dep.go"), "package dep\n\nfunc Factor() int { return "+depReturn+" }\n")
		writeFile(t, filepath.Join(root, "lib.go"), "package derivedstate\n\nimport \"example.com/dep\"\n\nfunc compute() int { "+libBody+" }\n")
		writeFile(t, filepath.Join(root, "unused", "unused.go"), "package unused\n\nfunc Uncalled() int { "+unusedBody+" }\n")
		writeFile(t, filepath.Join(root, "bench_test.go"), "package derivedstate\n\nimport \"testing\"\n\n//gofresh:pure\nfunc BenchmarkCompute(b *testing.B) {\n\tfor range b.N {\n\t\t_ = compute()\n\t}\n}\n")
	}
	writeFixture(dirA, "2", "return dep.Factor() + 1", "return 10")
	repo, err := gogit.PlainInit(dirA, false)
	if err != nil {
		t.Fatal(err)
	}
	commitAll(t, repo, "initial")
	withWorkingDir(t, dirA)

	closure.PruneMemos(0)
	t.Cleanup(func() { closure.PruneMemos(0) })

	const pkg = "example.com/derivedstate"
	const bench = "BenchmarkCompute"
	subject := gofresh.Subject{Package: pkg, Symbol: bench}
	env := testEnvironment(t, os.Environ())

	eCold, _, err := newEngineAt(t.Context(), dirA, dirA, false, env)
	if err != nil {
		t.Fatal(err)
	}
	fp, err := fixtureFacts(t.Context(), eCold, subject)
	if err != nil {
		t.Fatal(err)
	}
	rt, err := runtimeinput.Incomplete(dirA, "package-test-binary:derivedstate", "testlog lacks operation outcome evidence", os.Environ())
	if err != nil {
		t.Fatal(err)
	}
	stA := store.New(filepath.Join(dirA, "benchmarks"))
	recs := recordingtest.Results(bench, []float64{100}, recordingtest.Measured(fp, "ledger-placeholder", rt.Digest, rt.Manifest))
	if err := stA.Write("", bench, "", recs); err != nil {
		t.Fatal(err)
	}

	check := func(root string, st *store.Store) (verdict, string, gofresh.Fingerprint) {
		t.Helper()
		e, _, err := newEngineAt(t.Context(), root, root, false, env)
		if err != nil {
			t.Fatal(err)
		}
		batch, err := checkPackage(t.Context(), st, func(subjects []gofresh.Subject) (*gofresh.RuntimeBinding, error) {
			return newViewFor(e, t.Context(), subjects, root, gofresh.Measurement)
		}, pkg, "", root, []string{bench}, "", nil)
		if err != nil {
			t.Fatal(err)
		}
		bv := batch[bench]
		if bv == nil || bv.err != nil {
			t.Fatalf("checkPackage(%s): %+v", root, bv)
		}
		return bv.v, bv.reason, bv.current
	}

	// 1. Cold, warm, and discarded-cache recomputation agree on verdict and semantic facts.
	closure.PruneMemos(0)
	vCold, rCold, curCold := check(dirA, stA)
	vWarm, rWarm, curWarm := check(dirA, stA)
	closure.PruneMemos(0)
	vRecomp, rRecomp, curRecomp := check(dirA, stA)
	if vCold != verdictValid || vWarm != verdictValid || vRecomp != verdictValid || rCold != "" || rWarm != "" || rRecomp != "" {
		t.Fatalf("cold/warm/recompute verdicts = (%s,%q) (%s,%q) (%s,%q), want all valid", vCold, rCold, vWarm, rWarm, vRecomp, rRecomp)
	}
	if curCold.MaximalClosure != curWarm.MaximalClosure || curCold.MaximalClosure != curRecomp.MaximalClosure || curCold.Guards != curWarm.Guards || curCold.Guards != curRecomp.Guards {
		t.Fatalf("cold/warm/recompute semantic fingerprints diverged:\ncold: %+v\nwarm: %+v\nrecomp: %+v", curCold, curWarm, curRecomp)
	}

	// 2. Irrelevant declaration edit at the same commit preserves reuse.
	writeFixture(dirA, "2", "return dep.Factor() + 1", "return 999")
	if v, reason, _ := check(dirA, stA); v != verdictValid || reason != "" {
		t.Fatalf("irrelevant declaration edit invalidated benchmark: %s (%s)", v, reason)
	}

	// 3. Same-version selected content edit in a local dependency at the same
	// (commit, toolchain, buildconfig) tuple invalidates on both warm and
	// discarded-cache checks.
	writeFixture(dirA, "3", "return dep.Factor() + 1", "return 999")
	if v, reason, _ := check(dirA, stA); v != verdictStale || reason != "closure" {
		t.Fatalf("same-version selected dependency content edit was not stale (closure): %s (%s)", v, reason)
	}
	closure.PruneMemos(0)
	if v, reason, _ := check(dirA, stA); v != verdictStale || reason != "closure" {
		t.Fatalf("discarded-cache check after dependency edit = %s (%s), want stale (closure)", v, reason)
	}

	// Restore dirA and verify a second checkout coordinate (dirB) does not alias
	// dirA's memo when dirB's selected source changes.
	writeFixture(dirA, "2", "return dep.Factor() + 1", "return 10")
	dirB := t.TempDir()
	writeFixture(dirB, "2", "return dep.Factor() + 2", "return 10")
	stB := store.New(filepath.Join(dirB, "benchmarks"))
	if err := stB.Write("", bench, "", recs); err != nil {
		t.Fatal(err)
	}
	if v, _, _ := check(dirA, stA); v != verdictValid {
		t.Fatalf("restored dirA = %s, want valid", v)
	}
	withWorkingDir(t, dirB)
	if v, reason, _ := check(dirB, stB); v != verdictStale || reason != "closure" {
		t.Fatalf("distinct checkout coordinate dirB borrowed dirA's memo: %s (%s)", v, reason)
	}
	withWorkingDir(t, dirA)

	// 4. Budget-cut availability fails with context cancellation rather than
	// fabricating authority, and historical predating evidence is not upgraded.
	canceledCtx, cancel := context.WithCancel(t.Context())
	cancel()
	eValid, _, err := newEngineAt(t.Context(), dirA, dirA, false, env)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := storedVerdict(canceledCtx, stA, eValid, pkg, "", dirA, bench, ""); err == nil {
		t.Fatal("budget-cut context returned a completed verdict")
	}

	predating := fp
	predating.DynamicStateStrategy = ""
	predatingRecs := recordingtest.Results(bench, []float64{100}, recordingtest.Measured(predating, "ledger-placeholder", rt.Digest, rt.Manifest))
	if err := stA.Write("", bench, "legacy", predatingRecs); err != nil {
		t.Fatal(err)
	}
	var statusOut bytes.Buffer
	ctx, _, err := beginInvocation(t.Context(), stA.Root, nil, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if err := runStatus(ctx, &statusOut, stA.Root, "legacy", false, false, false, []string{"."}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(statusOut.String(), "stale") || !strings.Contains(statusOut.String(), "(dynamic-state strategy)") {
		t.Fatalf("historical predating recording was backfilled or upgraded:\n%s", statusOut.String())
	}
}
