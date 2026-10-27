package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	gogit "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/object"
	pew "github.com/greatliontech/pew"
	"github.com/greatliontech/pew/internal/compare"
	"github.com/greatliontech/pew/internal/run"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// tableRow is one verb's row of §12's verb defaults table: the
// backticked `--flag=value` defaults, and the `--flag` opt-ins with
// the parenthetical purpose stated right after the name; a one-word
// cross-reference ("(above)") is not a purpose.
type tableRow struct {
	defaults map[string]string
	optIns   []string
	purpose  map[string]string
}

// verbDefaultsTable parses §12's verb defaults table out of the spec,
// in row order.
func verbDefaultsTable(t *testing.T) (map[string]tableRow, []string) {
	t.Helper()
	spec, err := os.ReadFile(filepath.Join("..", "..", "docs", "specs", "spec.md"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(spec)
	start := strings.Index(text, "| verb | default behaviour and output |")
	if start < 0 {
		t.Fatal("spec §12 has no verb defaults table")
	}
	rows := map[string]tableRow{}
	var order []string
	flag := regexp.MustCompile("`--([a-z-]+)(?:=([^`]*))?`(?: \\(([^)]*)\\))?")
	for _, line := range strings.Split(text[start:], "\n")[2:] {
		if !strings.HasPrefix(line, "| `") {
			break
		}
		cells := strings.Split(line, " | ")
		if len(cells) != 4 {
			t.Fatalf("table row with %d cells: %q", len(cells), line)
		}
		verb := strings.Trim(strings.TrimPrefix(cells[0], "| "), "`")
		row := tableRow{defaults: map[string]string{}, purpose: map[string]string{}}
		for _, m := range flag.FindAllStringSubmatch(cells[2], -1) {
			row.defaults[m[1]] = m[2]
		}
		for _, m := range flag.FindAllStringSubmatch(cells[3], -1) {
			row.optIns = append(row.optIns, m[1])
			if len(strings.Fields(m[3])) >= 2 {
				row.purpose[m[1]] = strings.TrimSuffix(m[3], " |")
			}
		}
		rows[verb] = row
		order = append(order, verb)
	}
	return rows, order
}

// The verb defaults table is the CLI's contract (REQ-pew-verb-defaults):
// its verbs are the command set, each row's flags are the command's
// flag set, a default names the value the command holds when unset, an
// opt-in states its purpose on the first row naming it and is never
// also a default, and the guidance document's stated defaults and
// opt-in purposes agree.
func TestSurfaceTableTracksTheCommands(t *testing.T) {
	t.Parallel()
	table, order := verbDefaultsTable(t)
	stated := map[string]bool{}
	for _, verb := range order {
		row := table[verb]
		for _, name := range row.optIns {
			if _, isDefault := row.defaults[name]; isDefault {
				t.Errorf("%s: --%s is both a default and an opt-in", verb, name)
			}
			if !stated[name] && row.purpose[name] == "" {
				t.Errorf("%s: opt-in --%s states no purpose on the first row naming it", verb, name)
			}
			stated[name] = true
		}
	}
	commands := map[string]*cobra.Command{}
	visitLeafVerbs(newRootCmd(), func(name string, c *cobra.Command) { commands[name] = c })
	for verb := range table {
		if commands[verb] == nil {
			t.Errorf("table names verb %q the CLI lacks", verb)
		}
	}
	for verb, cmd := range commands {
		row, ok := table[verb]
		if !ok {
			t.Errorf("verb %q has no table row", verb)
			continue
		}
		listed := map[string]bool{}
		for name, value := range row.defaults {
			listed[name] = true
			f := cmd.Flags().Lookup(name)
			if f == nil {
				t.Errorf("%s: table default --%s is not a flag", verb, name)
				continue
			}
			if f.DefValue != value {
				t.Errorf("%s: --%s defaults to %q in code, %q in the table", verb, name, f.DefValue, value)
			}
		}
		for _, name := range row.optIns {
			listed[name] = true
			if cmd.Flags().Lookup(name) == nil {
				t.Errorf("%s: table opt-in --%s is not a flag", verb, name)
			}
		}
		cmd.Flags().VisitAll(func(f *pflag.Flag) {
			if f.Name == "help" || f.Hidden {
				return
			}
			if !listed[f.Name] {
				t.Errorf("%s: flag --%s is in neither the defaults nor the opt-ins of the table", verb, f.Name)
			}
		})
	}
	// The guidance document narrates the same defaults and opt-in
	// purposes: a knob text stating "(default X)" states the flag's own
	// literal; a flag whose default is derived at run time (an empty
	// DefValue, bench-dir) narrates the derivation in prose the flag
	// cannot vouch for; and every opt-in purpose stated in §12 appears
	// in that verb's guidance knob prose.
	doc, err := pew.GuidanceDocument()
	if err != nil {
		t.Fatal(err)
	}
	statedDefault := regexp.MustCompile(`\(default ([^)]*)\)`)
	for _, v := range doc.Verbs {
		cmd := commands[v.Name]
		if cmd == nil {
			continue
		}
		row := table[v.Name]
		knobs := map[string]string{}
		for _, k := range v.Knobs {
			knobs[k.Name] = strings.ReplaceAll(k.Text, "`", "")
			m := statedDefault.FindStringSubmatch(k.Text)
			if m == nil {
				continue
			}
			f := cmd.Flags().Lookup(k.Name)
			if f == nil || f.DefValue == "" {
				continue
			}
			if want := strings.Trim(f.DefValue, `"`); m[1] != want {
				t.Errorf("guidance %s.%s states default %q; the flag holds %q", v.Name, k.Name, m[1], want)
			}
		}
		for name, purpose := range row.purpose {
			if !strings.Contains(strings.ToLower(knobs[name]), strings.ToLower(purpose)) {
				t.Errorf("guidance %s.%s does not carry §12 stated purpose %q: got %q", v.Name, name, purpose, knobs[name])
			}
		}
	}
	// Contextual policy defaults and derived confidence on stat (§10.1, §12):
	// informational defaults are partial/report/report; with --fail-on-regression,
	// omitted policies resolve to complete/require/compatible while explicit
	// overrides are preserved; omitted --confidence derives from 1 - alpha.
	for _, tc := range []struct {
		args       []string
		wantPolicy compare.Policies
		wantConf   float64
	}{
		{nil, compare.Policies{Coverage: "partial", Freshness: "report", Conditions: "report"}, 0.95},
		{[]string{"--fail-on-regression"}, compare.Policies{Coverage: "complete", Freshness: "require", Conditions: "compatible"}, 0.95},
		{[]string{"--fail-on-regression", "--coverage=partial"}, compare.Policies{Coverage: "partial", Freshness: "require", Conditions: "compatible"}, 0.95},
		{[]string{"--fail-on-regression", "--freshness=report", "--conditions=report"}, compare.Policies{Coverage: "complete", Freshness: "report", Conditions: "report"}, 0.95},
		{[]string{"--alpha=0.01"}, compare.Policies{Coverage: "partial", Freshness: "report", Conditions: "report"}, 0.99},
		{[]string{"--alpha=0.01", "--confidence=0.90"}, compare.Policies{Coverage: "partial", Freshness: "report", Conditions: "report"}, 0.90},
	} {
		var sc statConfig
		sc.opts = compare.DefaultOptions()
		cmd := &cobra.Command{Use: "stat"}
		f := cmd.Flags()
		f.Float64Var(&sc.opts.Alpha, "alpha", sc.opts.Alpha, "")
		f.Float64Var(&sc.opts.Confidence, "confidence", sc.opts.Confidence, "")
		f.BoolVar(&sc.failOnRegression, "fail-on-regression", false, "")
		f.StringVar(&sc.opts.Policies.Coverage, "coverage", "partial", "")
		f.StringVar(&sc.opts.Policies.Freshness, "freshness", "report", "")
		f.StringVar(&sc.opts.Policies.Conditions, "conditions", "report", "")
		if err := cmd.ParseFlags(tc.args); err != nil {
			t.Fatalf("ParseFlags(%v): %v", tc.args, err)
		}
		resolveStatFlags(&sc, cmd)
		if sc.opts.Policies != tc.wantPolicy {
			t.Errorf("resolveStatFlags(%v) policies = %+v, want %+v", tc.args, sc.opts.Policies, tc.wantPolicy)
		}
		if sc.opts.Confidence != tc.wantConf {
			t.Errorf("resolveStatFlags(%v) confidence = %v, want %v", tc.args, sc.opts.Confidence, tc.wantConf)
		}
	}
}

// run's default output carries the served count per package
// (REQ-pew-verb-defaults, §12): over two recorded benchmarks with one
// added, the run serves two and measures one, saying so on one line,
// and an unchanged tree says nothing to run with its count.
func TestRunReportsServedBenchmarks(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("measures a fixture package through the real executor")
	}
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.com/served\n\ngo 1.26.4\n",
		// Empty bodies keep the fixture's closure trivial; the served
		// count needs a valid recording to serve, and the serve of a real
		// body is TestRunServesValidRecordingsByDefault's
		// (REQ-pew-serve-proven).
		"a/a_test.go": "package a\n\nimport \"testing\"\n\nfunc BenchmarkOne(b *testing.B) {}\n\nfunc BenchmarkTwo(b *testing.B) {}\n",
		"b/b_test.go": "package b\n\nimport \"testing\"\n\nfunc BenchmarkSolo(b *testing.B) {}\n",
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
	ctx := withWorkingDir(t, dir)
	rc := runConfig{
		benchDir: filepath.Join(dir, "benchmarks"),
		opts:     run.Options{Count: 1, Benchtime: "1x", Bench: "."},
		throttle: func() run.ThrottleSnapshot { return run.ThrottleSnapshot{"c0": 1} },
	}
	var out bytes.Buffer
	if err := runRun(ctx, &out, &bytes.Buffer{}, rc, []string{"./..."}); err != nil {
		t.Fatalf("first run: %v\n%s", err, out.String())
	}
	if strings.Count(out.String(), "recorded     ") != 3 || strings.Contains(out.String(), "served       ") {
		t.Fatalf("first run over an unrecorded tree:\n%s", out.String())
	}
	out.Reset()
	if err := runRun(ctx, &out, &bytes.Buffer{}, rc, []string{"./..."}); err != nil {
		t.Fatalf("second run: %v\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "served       example.com/served/a: 2 valid, nothing to run") || !strings.Contains(out.String(), "served       example.com/served/b: 1 valid, nothing to run") {
		t.Fatalf("unchanged tree:\n%s", out.String())
	}
	// Add a benchmark and commit: the two recorded ones serve through
	// the inert-growth rule (§7.9), the new one measures. (An edited
	// sibling body would stale the whole compartment — that is §7.9's
	// contract, not a served-count case.)
	edited := files["a/a_test.go"] + "\nfunc BenchmarkThree(b *testing.B) {}\n"
	if err := os.WriteFile(filepath.Join(dir, "a", "a_test.go"), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b", "b_test.go"), []byte(files["b/b_test.go"]+"\nfunc BenchmarkDuo(b *testing.B) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	repo, err := gogit.PlainOpen(dir)
	if err != nil {
		t.Fatal(err)
	}
	wt, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if err := wt.AddGlob("."); err != nil {
		t.Fatal(err)
	}
	if _, err := wt.Commit("edit", &gogit.CommitOptions{Author: &object.Signature{Name: "t", Email: "t@example.invalid", When: time.Unix(2, 0)}}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := runRun(ctx, &out, &bytes.Buffer{}, rc, []string{"./..."}); err != nil {
		t.Fatalf("third run: %v\n%s", err, out.String())
	}
	got := out.String()
	if !strings.Contains(got, "served       example.com/served/a: 2 valid, measuring 1") || !strings.Contains(got, "served       example.com/served/b: 1 valid, measuring 1") || strings.Count(got, "recorded     ") != 2 || !strings.Contains(got, "recorded     example.com/served/a.BenchmarkThree") || !strings.Contains(got, "recorded     example.com/served/b.BenchmarkDuo") {
		t.Fatalf("grown tree:\n%s", got)
	}
}
