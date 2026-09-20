package run

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/greatliontech/gofresh/guard"
	"golang.org/x/perf/benchfmt"
)

type specKeyRow struct {
	name, class, display string
	audit, guard         bool
	chunked              bool
}

// specKeyTable parses spec §5's key table — the header row naming the
// class, audit?, guard?, and display columns, then every row to the
// first non-table line — into rows in table order.
func specKeyTable(t *testing.T, spec string) []specKeyRow {
	t.Helper()
	lines := strings.Split(spec, "\n")
	start := -1
	for i, l := range lines {
		if strings.HasPrefix(l, "| key ") && strings.Contains(l, "| class") && strings.Contains(l, "| audit? ") && strings.Contains(l, "| guard? ") && strings.Contains(l, "| chunked? ") && strings.Contains(l, "| display") {
			start = i
			break
		}
	}
	if start < 0 || !strings.HasPrefix(lines[start+1], "|---") {
		t.Fatal("spec §5 key table with class, audit?, guard?, chunked?, and display columns not found")
	}
	var rows []specKeyRow
	for _, l := range lines[start+2:] {
		if !strings.HasPrefix(l, "|") {
			break
		}
		cells := strings.Split(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(l), "|"), "|"), "|")
		if len(cells) != 8 {
			t.Fatalf("spec §5 row has %d cells, want 8 (key, meaning, source, class, audit?, guard?, chunked?, display): %q", len(cells), l)
		}
		name := strings.Trim(strings.TrimSpace(cells[0]), "`")
		yesNo := func(column string, cell string) bool {
			switch v := strings.TrimSpace(cell); v {
			case "yes":
				return true
			case "no":
				return false
			default:
				t.Fatalf("spec §5 row %s: %s cell %q is neither yes nor no", name, column, v)
				return false
			}
		}
		rows = append(rows, specKeyRow{name: name, class: strings.TrimSpace(cells[3]), audit: yesNo("audit?", cells[4]), guard: yesNo("guard?", cells[5]), chunked: yesNo("chunked?", cells[6]), display: strings.TrimSpace(cells[7])})
	}
	return rows
}

// keyShaped reports whether s could be a recording key: non-empty,
// lowercase letters and dashes only, as every row's name is.
func keyShaped(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r == '-') {
			return false
		}
	}
	return true
}

func className(c RecordingKeyClass) string {
	switch c {
	case KeyDiscriminator:
		return "discriminator"
	case KeyMandatory:
		return "mandatory"
	case KeyOmittable:
		return "omittable"
	}
	return "?"
}

// TestRecordingKeysMirrorSpec binds the registry to spec §5's key table
// as the document states it — every row, in order, with its class,
// audit and guard marks, and display name — so the registry, and everything derived from it (the
// store's shape and closed-set checks, admission, compare's grouping
// projection and audit notes, the stream's reserved-key refusal), moves
// only together with the spec (REQ-pew-key-set).
func TestRecordingKeysMirrorSpec(t *testing.T) {
	spec, err := os.ReadFile(filepath.Join("..", "..", "docs", "specs", "spec.md"))
	if err != nil {
		t.Fatal(err)
	}
	rows := specKeyTable(t, string(spec))
	if len(rows) != len(RecordingKeys) {
		t.Fatalf("spec §5 lists %d keys, the registry %d", len(rows), len(RecordingKeys))
	}
	for i, row := range rows {
		k := RecordingKeys[i]
		if k.Name != row.name {
			t.Errorf("row %d: spec %q, registry %q (order or membership)", i, row.name, k.Name)
			continue
		}
		if className(k.Class) != row.class {
			t.Errorf("%s: spec class %q, registry %q", k.Name, row.class, className(k.Class))
		}
		if k.Audit != row.audit {
			t.Errorf("%s: spec audit %v, registry %v", k.Name, row.audit, k.Audit)
		}
		if k.Guard != row.guard {
			t.Errorf("%s: spec guard %v, registry %v", k.Name, row.guard, k.Guard)
		}
		if k.Chunked != row.chunked {
			t.Errorf("%s: spec chunked %v, registry %v", k.Name, row.chunked, k.Chunked)
		}
		if k.Display != row.display {
			t.Errorf("%s: spec display %q, registry %q", k.Name, row.display, k.Display)
		}
	}
	// The projections are the rows' own.
	if got := len(RecordingConfigKeys); got != len(RecordingKeys) {
		t.Errorf("RecordingConfigKeys has %d names, the registry %d rows", got, len(RecordingKeys))
	}
	for _, name := range MandatoryRecordingKeys {
		if registered(name).Class != KeyMandatory {
			t.Errorf("MandatoryRecordingKeys lists %s, class %s", name, className(registered(name).Class))
		}
	}
	if !IsRecordingKey(KeyClosure.Name) || IsRecordingKey("pew-unknown") || IsRecordingKey("goos") {
		t.Error("IsRecordingKey is not the registry's membership")
	}
	if !IsToolchainKey("cpu") || IsToolchainKey(KeyCommit.Name) {
		t.Error("IsToolchainKey is not ToolchainKeys' membership")
	}
}

// recordingPackages are the packages whose job is recordings — where a
// bare row name (commit, dirty, machine, toolchain are ordinary words)
// is a key spelling; elsewhere only the namespace arm applies. The
// match is the exact directory: a new package handling recordings
// joins this list, or its bare spellings are seen by the namespace arm
// alone.
var recordingPackages = []string{"internal/run", "internal/store", "internal/compare", "cmd/pew"}

// spelledNotAsKey lists, per module-relative file and literal, with its
// reason, a bare row-name literal in a recording package that is not
// the key (a git argument, a flag name): the remedy when this pin
// refuses an ordinary word, never a rename to the row — local to the
// one site that earned it, so an exemption never licenses a hand
// spelling elsewhere. Empty today.
var spelledNotAsKey = map[string]map[string]string{}

const builderImport = `"github.com/greatliontech/pew/internal/recordingtest"`

// keySpellingOffenders judges one production file (rel its
// module-relative path): a key-shaped literal in the `pew-` namespace
// (the `pew-ab*` artifact keys of §12's other artifact class excepted;
// a temp-file pattern or a line prefix is not key-shaped), a literal
// equal to a row's name inside a recording package, and an import of
// the test builder.
func keySpellingOffenders(t *testing.T, fset *token.FileSet, f *ast.File, rel string) []string {
	t.Helper()
	inRecordingPackage := false
	for _, p := range recordingPackages {
		if filepath.ToSlash(filepath.Dir(rel)) == p {
			inRecordingPackage = true
		}
	}
	var offenders []string
	for _, imp := range f.Imports {
		if imp.Path.Value == builderImport {
			offenders = append(offenders, fset.Position(imp.Pos()).String()+": production code imports the test builder")
		}
	}
	ast.Inspect(f, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		v, err := strconv.Unquote(lit.Value)
		if err != nil {
			return true
		}
		namespaced := keyShaped(v) && strings.HasPrefix(v, RecordingKeyNamespace) && !strings.HasPrefix(v, RecordingKeyNamespace+"ab")
		reason, exempt := spelledNotAsKey[rel][v]
		if exempt && reason == "" {
			t.Fatalf("%s: %s is exempted without a reason", rel, lit.Value)
		}
		bare := inRecordingPackage && IsRecordingKey(v) && !exempt
		if namespaced || bare {
			offenders = append(offenders, fset.Position(lit.Pos()).String()+": "+lit.Value)
		}
		return true
	})
	return offenders
}

// TestRecordingKeySpellingsLiveInTheRegistry walks the module's
// production sources and refuses, outside the registry file — the one
// home of every spelling (REQ-pew-key-set) — every offender
// keySpellingOffenders names: a namespaced key spelling anywhere, a
// bare row name in a recording package, an import of the test builder.
func TestRecordingKeySpellingsLiveInTheRegistry(t *testing.T) {
	moduleRoot := filepath.Join("..", "..")
	registry := filepath.Join(moduleRoot, "internal", "run", "registry.go")
	fset := token.NewFileSet()
	var offenders []string
	err := filepath.WalkDir(moduleRoot, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if name := d.Name(); name == "fixtures" || name == "testdata" || strings.HasPrefix(name, ".") && path != moduleRoot {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") || path == registry {
			return nil
		}
		rel, err := filepath.Rel(moduleRoot, path)
		if err != nil {
			return err
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		offenders = append(offenders, keySpellingOffenders(t, fset, f, filepath.ToSlash(rel))...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) > 0 {
		t.Errorf("recording keys spelled, or the test builder imported, outside the registry (read the row through run.Key*.Name; a bare word that is not the key goes in spelledNotAsKey under its file with its reason):\n  %s", strings.Join(offenders, "\n  "))
	}
}

// TestKeySpellingWalkSeesEachArm pins the walk's judgment over
// synthetic sources — the on-disk walk sees no probe (it reads the
// tree, not an overlay), so each arm is witnessed here: the namespace
// arm, the bare-word arm inside and outside a recording package, the
// builder import, and what is not key-shaped.
func TestKeySpellingWalkSeesEachArm(t *testing.T) {
	fset := token.NewFileSet()
	judge := func(rel, src string) []string {
		t.Helper()
		f, err := parser.ParseFile(fset, rel, "package p\n"+src, 0)
		if err != nil {
			t.Fatal(err)
		}
		return keySpellingOffenders(t, fset, f, rel)
	}
	cases := []struct {
		name, rel, src string
		want           int
	}{
		{"namespaced anywhere", "internal/other/x.go", `var k = "pew-thing"`, 1},
		{"temp pattern is not a key", "internal/other/x.go", `var k = "pew-testbin-*"`, 0},
		{"artifact key exempt", "cmd/pew/x.go", `var k = "pew-ab-ref"`, 0},
		{"bare word in a recording package", "cmd/pew/x.go", `var k = "commit"`, 1},
		{"bare word outside", "internal/other/x.go", `var k = "commit"`, 0},
		{"builder import", "internal/other/x.go", "import " + builderImport, 1},
	}
	for _, c := range cases {
		if got := judge(c.rel, c.src); len(got) != c.want {
			t.Errorf("%s: %d offenders, want %d: %v", c.name, len(got), c.want, got)
		}
	}
}

// TestVerifyToolchainConfigCoversEveryToolchainKey: the value-trust arm
// partitions ToolchainKeys by hand (out-of-band truth for goos, goarch,
// and pkg; in-stream consistency for cpu), so a key joining the list
// without joining an arm would be kept by the closed set and verified
// by nothing — every key must be refused by one arm.
func TestVerifyToolchainConfigCoversEveryToolchainKey(t *testing.T) {
	truth := ToolchainTruth{GOOS: "linux", GOARCH: "amd64", ImportPath: "example.com/p"}
	for _, key := range ToolchainKeys {
		row := func(v string) *benchfmt.Result {
			return &benchfmt.Result{Config: []benchfmt.Config{{Key: key, Value: []byte(v), File: true}}}
		}
		// A value the truth disagrees with; for a key verified by
		// consistency alone, two rows disagreeing with each other.
		if err := VerifyToolchainConfig([]*benchfmt.Result{row("forged-a"), row("forged-b")}, truth); err == nil {
			t.Errorf("toolchain key %s is verified by no arm of VerifyToolchainConfig", key)
		}
	}
}

// TestGuardConfigFollowsTheGuardRows: the A/B path's guard lines are the
// registry's guard rows in table order, each carrying its own field —
// the precedence §5 states for an A/B refusal, pinned at its source
// (a literal expectation over a fully populated Guards).
func TestGuardConfigFollowsTheGuardRows(t *testing.T) {
	cfgs := GuardConfig(guard.Guards{Toolchain: "t", Machine: "m", BuildConfig: "b", RuntimeConfig: "r"})
	if len(cfgs) != len(GuardRecordingKeys) || len(cfgs) != 4 {
		t.Fatalf("GuardConfig emitted %d lines for %d guard rows", len(cfgs), len(GuardRecordingKeys))
	}
	want := []struct{ key, value string }{{"toolchain", "t"}, {"machine", "m"}, {"buildconfig", "b"}, {"runtimeconfig", "r"}}
	for i, w := range want {
		if cfgs[i].Key != w.key || string(cfgs[i].Value) != w.value || !cfgs[i].File {
			t.Errorf("line %d = %s: %q (file=%v), want %s: %q", i, cfgs[i].Key, cfgs[i].Value, cfgs[i].File, w.key, w.value)
		}
		if GuardRecordingKeys[i].Name != w.key {
			t.Errorf("guard row %d is %s, want %s", i, GuardRecordingKeys[i].Name, w.key)
		}
	}
}
