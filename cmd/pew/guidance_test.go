package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh/guidance"
	pew "github.com/greatliontech/pew"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// The embedded guidance document parses — a malformed document would
// panic every serving surface, so this is the loud build-time pin
// (REQ-pew-guidance).
func TestGuidanceDocumentParses(t *testing.T) {
	if _, err := pew.GuidanceDocument(); err != nil {
		t.Fatal(err)
	}
}

// The CLI surface and the guidance document cannot drift: every
// visible leaf command's spelling and every local flag is documented,
// in both directions, judged over the real cobra tree, and served
// Short/Long ARE the document's projections (REQ-pew-guidance).
// Cobra's help/completion plumbing is surface plumbing, not verbs.
func TestGuidanceCoversTheCLISurface(t *testing.T) {
	doc, err := pew.GuidanceDocument()
	if err != nil {
		t.Fatal(err)
	}
	registered := registeredCLI(newRootCmd())
	defects, err := doc.Coverage("cli", registered)
	if err != nil || len(defects) != 0 {
		t.Fatalf("cli coverage: err=%v defects:\n%s", err, strings.Join(defects, "\n"))
	}
	root := newRootCmd()
	for name := range registered {
		c, _, err := root.Find(strings.Fields(name))
		if err != nil {
			t.Fatalf("find %q: %v", name, err)
		}
		short, err := doc.Description("cli", name)
		if err != nil {
			t.Errorf("%q: %v", name, err)
			continue
		}
		if c.Short != short {
			t.Errorf("%q Short diverged:\ncli %q\ndoc %q", name, c.Short, short)
		}
		if c.Long != "" {
			help, err := doc.Help("cli", name)
			if err != nil || c.Long != help {
				t.Errorf("%q Long diverged from Help (err=%v):\ncli %q\ndoc %q", name, err, c.Long, help)
			}
			if strings.Contains(c.Long, "\nknobs:") {
				t.Errorf("%q Long carries the knobs block beside cobra's Flags", name)
			}
		}
	}
}

// The guidance command serves the document: a verb's full section,
// the decision map for no verb, and a teaching error for an unknown
// one (REQ-pew-guidance).
func TestGuidanceCommandServesTheDocument(t *testing.T) {
	doc, err := pew.GuidanceDocument()
	if err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) (string, error) {
		t.Helper()
		root := newRootCmd()
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetArgs(args)
		err := root.Execute()
		return out.String(), err
	}
	got, err := run("guidance", "stat")
	if err != nil {
		t.Fatal(err)
	}
	long, _ := doc.Long("cli", "stat")
	if strings.TrimSuffix(got, "\n") != long {
		t.Fatalf("guidance stat diverged:\n%q\nwant\n%q", got, long)
	}
	got, err = run("guidance")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSuffix(got, "\n") != doc.Orientation() {
		t.Fatalf("guidance orientation diverged: %q", got)
	}
	if _, err = run("guidance", "vanished"); err == nil || !strings.Contains(err.Error(), "decision map") {
		t.Fatalf("unknown verb: err = %v", err)
	}
}

// zeroDefault reports whether pflag prints no default for the flag —
// its printing rule: the default is the zero value of the flag's own
// type, so a string "0" is a printed default while an int 0 is not,
// and the fallback for other types is pflag's own set of zero spellings.
func zeroDefault(f *pflag.Flag) bool {
	switch f.Value.Type() {
	case "string":
		return f.DefValue == ""
	case "bool":
		return f.DefValue == "false"
	case "int", "int64":
		return f.DefValue == "0"
	case "duration":
		return f.DefValue == "0" || f.DefValue == "0s"
	case "stringArray", "stringSlice":
		return f.DefValue == "[]"
	case "float64":
		return f.DefValue == "0"
	}
	switch f.DefValue {
	case "", "0", "false", "<nil>":
		return true
	}
	return false
}

// TestZeroDefaultFollowsPflagsPrintingRule pins the registration fact's
// predicate against pflag's own rendering: for every flag, pflag prints
// a "(default X)" exactly when the predicate says the default is
// non-zero — the library is the oracle, the named rows at each type's
// cliff the belt (a string "0" prints, an int 0 does not).
func TestZeroDefaultFollowsPflagsPrintingRule(t *testing.T) {
	fs := pflag.NewFlagSet("t", pflag.ContinueOnError)
	fs.String("s0", "", "")
	fs.String("s1", "0", "")
	fs.Bool("b0", false, "")
	fs.Bool("b1", true, "")
	fs.Int("i0", 0, "")
	fs.Int("i1", 6, "")
	fs.Float64("f0", 0, "")
	fs.Float64("f1", 0.05, "")
	fs.Duration("d0", 0, "")
	fs.Duration("d1", 1e9, "")
	fs.StringArray("a0", nil, "")
	fs.StringArray("a1", []string{"x"}, "")
	fs.StringSlice("l0", nil, "")
	want := map[string]bool{"s0": true, "s1": false, "b0": true, "b1": false, "i0": true, "i1": false, "f0": true, "f1": false, "d0": true, "d1": false, "a0": true, "a1": false, "l0": true}
	for name, zero := range want {
		f := fs.Lookup(name)
		if got := zeroDefault(f); got != zero {
			t.Errorf("zeroDefault(%s: %s %q) = %v, want %v", name, f.Value.Type(), f.DefValue, got, zero)
		}
	}
	fs.VisitAll(func(f *pflag.Flag) {
		one := pflag.NewFlagSet("one", pflag.ContinueOnError)
		one.AddFlag(f)
		prints := strings.Contains(one.FlagUsages(), "(default ")
		if zeroDefault(f) == prints {
			t.Errorf("zeroDefault(%s: %s %q) = %v while pflag prints a default: %v", f.Name, f.Value.Type(), f.DefValue, zeroDefault(f), prints)
		}
	})
}

// TestCoverageJudgesTheRegisteredDefaultFact pins the fact the coverage
// pin registers, in both directions, over a document that spells a
// default outside the (default X) form: registered with a printed
// default the CLI lint names the knob; registered without one it is
// silent — so every pew knob's registration is load-bearing exactly
// where its prose would print a default twice.
func TestCoverageJudgesTheRegisteredDefaultFact(t *testing.T) {
	doc, err := guidance.Parse([]byte("# t — tool-resident guidance\n\n## verbs\n\n### v\n**surfaces:** cli\n**does:** does a thing.\n**knobs:**\n- `k` — how many, default 6 unless given.\n**when:** whenever.\n**example:** `t v`.\n\n## decision map\n- to do a thing: `v`.\n"))
	if err != nil {
		t.Fatal(err)
	}
	defects, err := doc.Coverage("cli", map[string]guidance.Registered{"v": {"k": true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(defects) != 1 || !strings.Contains(defects[0], `"k"`) || !strings.Contains(defects[0], "default") {
		t.Fatalf("a printed default beside a spelled one reported %v, want the one knob named", defects)
	}
	defects, err = doc.Coverage("cli", map[string]guidance.Registered{"v": {"k": false}})
	if err != nil || len(defects) != 0 {
		t.Fatalf("a zero default reported %v, %v; want silence", defects, err)
	}
}

// registeredCLI is the CLI surface as the coverage judgment sees it:
// every non-hidden leaf verb by its spelling, each flag mapped to the
// non-zero-default fact the CLI lint judges — a flag cobra prints a
// default for must not spell one in its knob prose.
func registeredCLI(root *cobra.Command) map[string]guidance.Registered {
	registered := map[string]guidance.Registered{}
	var walk func(prefix string, c *cobra.Command)
	walk = func(prefix string, c *cobra.Command) {
		for _, child := range c.Commands() {
			if child.Hidden || child.Name() == "help" || child.Name() == "completion" {
				continue
			}
			name := strings.TrimSpace(prefix + " " + child.Name())
			if child.HasSubCommands() {
				walk(name, child)
				continue
			}
			flags := guidance.Registered{}
			child.LocalFlags().VisitAll(func(f *pflag.Flag) {
				if f.Name == "help" {
					return
				}
				flags[f.Name] = !zeroDefault(f)
			})
			registered[name] = flags
		}
	}
	walk("", root)
	return registered
}

// TestRegisteredCLICarriesEachFlagsDefaultFact pins the registration
// the coverage judgment is handed, over the real command tree: a flag
// with a printed default registers true, one with a zero default false.
func TestRegisteredCLICarriesEachFlagsDefaultFact(t *testing.T) {
	registered := registeredCLI(newRootCmd())
	for _, row := range []struct {
		verb, flag string
		prints     bool
	}{{"run", "count", true}, {"ab", "bench", true}, {"ab", "strict", false}, {"status", "explain", false}} {
		got, ok := registered[row.verb][row.flag]
		if !ok || got != row.prints {
			t.Errorf("registered[%s][%s] = %v (registered: %v), want %v", row.verb, row.flag, got, ok, row.prints)
		}
	}
}
