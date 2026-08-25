package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gofresh "github.com/greatliontech/gofresh"
	"github.com/greatliontech/gofresh/runtimeinput"
	"golang.org/x/perf/benchfmt"

	runpkg "github.com/greatliontech/pew/internal/run"
	"github.com/greatliontech/pew/internal/store"
)

// TestCheckOneServesInertTestSuiteGrowth pins the inert-growth verdict rule
// end to end (spec §7.9): adding a sibling test beside a recorded benchmark
// moves only the package's test-variant compartment, so the recording still
// reads valid — proven by the recorded ledger diffing inert and the
// pin-refreshed fingerprint re-checking — and the run path rewrites the
// recording under the refreshed pin so later verdicts read plainly valid; a
// non-inert movement (an added init function) refuses; and a pin that moved
// with the compartment (a forged toolchain) cannot hide behind the
// compartment verdict.
func TestCheckOneServesInertTestSuiteGrowth(t *testing.T) {
	tmp := t.TempDir()
	for name, content := range map[string]string{
		"go.mod": "module example.com/grow\n\ngo 1.24\n",
		"lib.go": "package grow\n\nfunc Value() int { return 40 + 2 }\n",
		"bench_test.go": "package grow\n\nimport \"testing\"\n\n" +
			"//gofresh:pure\nfunc BenchmarkValue(b *testing.B) {\n\tfor i := 0; i < b.N; i++ {\n\t\t_ = Value()\n\t}\n}\n",
	} {
		if err := os.WriteFile(filepath.Join(tmp, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	e, err := gofresh.New(gofresh.WithDir(tmp))
	if err != nil {
		t.Fatal(err)
	}
	const pkg = "example.com/grow"
	const bench = "BenchmarkValue"
	subject := gofresh.Subject{Package: pkg, Symbol: bench}
	view, err := e.NewViewFor(context.Background(), []gofresh.Subject{subject}, tmp, gofresh.Measurement)
	if err != nil {
		t.Fatal(err)
	}
	fp, err := view.Capture(context.Background(), subject)
	if err != nil {
		t.Fatal(err)
	}
	ledger, err := view.TestVariantLedger(subject)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := runpkg.EncodeLedger(runpkg.LedgerFromGofresh(ledger))
	if err != nil {
		t.Fatal(err)
	}
	rt, err := runtimeinput.Incomplete(tmp, "package-test-binary:grow", "testlog lacks operation outcome evidence")
	if err != nil {
		t.Fatal(err)
	}
	st := store.New(t.TempDir())
	write := func(toolchain string) {
		t.Helper()
		cfg := []benchfmt.Config{
			{Key: "pew-format", Value: []byte(runpkg.RecordingFormat), File: true},
			{Key: "commit", Value: []byte("c1"), File: true},
			{Key: "toolchain", Value: []byte(toolchain), File: true},
			{Key: "machine", Value: []byte(fp.Guards.Machine), File: true},
			{Key: "buildconfig", Value: []byte(fp.Guards.BuildConfig), File: true},
			{Key: "runtimeconfig", Value: []byte(fp.Guards.RuntimeConfig), File: true},
			{Key: "pew-runconditions", Value: []byte("governor=performance turbo=off load1=0.03 throttled=false battery=false"), File: true},
			{Key: "pew-closure", Value: []byte(fp.MaximalClosure), File: true},
			{Key: "pew-dynamic-state", Value: []byte(fp.DynamicStateStrategy), File: true},
			{Key: "pew-test-variants", Value: []byte(fp.TestVariantClosure), File: true},
			{Key: "pew-test-variant-ledger", Value: []byte(encoded), File: true},
			{Key: "pew-runtime", Value: []byte(rt.Digest), File: true},
			{Key: "pew-runtime-inputs", Value: []byte(rt.Manifest), File: true},
			{Key: "pew-purity", Value: []byte(fp.PurityAssertion), File: true},
			{Key: "dirty", Value: []byte("false"), File: true},
		}
		recs := []*benchfmt.Result{{Name: benchfmt.Name(bench), Iters: 1, Values: []benchfmt.Value{{Value: 1, Unit: "sec/op"}}, Config: cfg}}
		if err := st.Write("", bench, "", recs); err != nil {
			t.Fatal(err)
		}
	}
	write(fp.Guards.Toolchain)

	v, reason, _, grown, err := checkOne(st, e, pkg, "", tmp, bench, "")
	if err != nil || v != verdictValid || grown != "" {
		t.Fatalf("baseline verdict = {%s %q grown=%q}, %v; want plainly valid", v, reason, grown, err)
	}

	// An added sibling test in a new file is an inert compartment movement:
	// the recording still reads valid, through the rule.
	if err := os.WriteFile(filepath.Join(tmp, "more_test.go"),
		[]byte("package grow\n\nimport \"testing\"\n\nfunc TestMore(t *testing.T) {\n\tif Value() != 42 {\n\t\tt.Fail()\n\t}\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	v, reason, servedFP, grown, err := checkOne(st, e, pkg, "", tmp, bench, "")
	if err != nil || v != verdictValid || grown == "" {
		t.Fatalf("grown verdict = {%s %q grown=%q}, %v; want valid through the inert-growth rule", v, reason, grown, err)
	}
	if servedFP.TestVariantClosure == fp.TestVariantClosure {
		t.Fatal("served fingerprint did not carry the refreshed compartment pin")
	}

	// The run path rewrites the recording under the refreshed pin: nonValid
	// reports nothing to run and the next verdict is plainly valid. The
	// rewrite lands under the recording store, which the repository-state
	// bracket excludes wholesale (spec §5) — no per-write registration.
	need, err := nonValid(io.Discard, st, e, pkg, "", tmp, "", []string{bench})
	if err != nil || len(need) != 0 {
		t.Fatalf("nonValid after inert growth = %v, %v; want none", need, err)
	}
	recs, err := st.Read("", bench, "")
	if err != nil {
		t.Fatal(err)
	}
	rewritten := map[string]string{}
	for _, c := range recs[0].Config {
		rewritten[c.Key] = string(c.Value)
	}
	if rewritten["pew-test-variants"] != servedFP.TestVariantClosure || rewritten["pew-test-variant-ledger"] == encoded {
		t.Fatalf("run path did not rewrite the recording under the refreshed pin: %q", rewritten["pew-test-variants"])
	}
	v, reason, _, grown, err = checkOne(st, e, pkg, "", tmp, bench, "")
	if err != nil || v != verdictValid || grown != "" {
		t.Fatalf("post-rewrite verdict = {%s %q grown=%q}, %v; want plainly valid", v, reason, grown, err)
	}

	// A pin moved together with the compartment cannot hide behind the
	// compartment verdict: the rule's re-check surfaces it.
	if err := os.WriteFile(filepath.Join(tmp, "even_more_test.go"),
		[]byte("package grow\n\nimport \"testing\"\n\nfunc TestEvenMore(t *testing.T) {\n\tif Value() == 0 {\n\t\tt.Fail()\n\t}\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	write("go0.0-never")
	v, reason, _, grown, err = checkOne(st, e, pkg, "", tmp, bench, "")
	if err != nil || v != verdictStale || reason != "toolchain" || grown != "" {
		t.Fatalf("masked-pin verdict = {%s %q grown=%q}, %v; want stale (toolchain) — the refreshed verdict's own attribution", v, reason, grown, err)
	}
	write(fp.Guards.Toolchain)

	// An added init function is not inert: the rule refuses and the
	// ordinary stale verdict stands.
	if err := os.WriteFile(filepath.Join(tmp, "init_test.go"),
		[]byte("package grow\n\nfunc init() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	v, reason, refusedFP, grown, err := checkOne(st, e, pkg, "", tmp, bench, "")
	if err != nil || v != verdictStale || reason != "test variants" || grown != "" {
		t.Fatalf("non-inert verdict = {%s %q grown=%q}, %v; want stale (test variants)", v, reason, grown, err)
	}
	// The explanation lays the compartment pin side by side (spec §12): a
	// stale "test variants" verdict shows its moved input, never an
	// all-matching table that contradicts the verdict.
	var explained bytes.Buffer
	explainRecordAgainstCurrent(&explained, e, tmp, pkg, bench, refusedFP, os.Environ())
	variantRow := ""
	for _, line := range strings.Split(explained.String(), "\n") {
		if strings.Contains(line, "test-variants") {
			variantRow = line
		}
	}
	if variantRow == "" || !strings.HasSuffix(strings.TrimSpace(variantRow), "NO") {
		t.Fatalf("explanation does not surface the moved compartment pin:\n%s", explained.String())
	}
}
