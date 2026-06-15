package closure

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHashFiles(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("a.go", "package p\n")
	write("b.go", "package p\nvar X = 1\n")

	h1, err := hashFiles(dir, []string{"a.go", "b.go"})
	if err != nil {
		t.Fatal(err)
	}
	if len(h1) != 16 {
		t.Errorf("hash len: got %d", len(h1))
	}

	// Order-insensitive (files sorted internally).
	if h2, _ := hashFiles(dir, []string{"b.go", "a.go"}); h2 != h1 {
		t.Errorf("hash not order-insensitive: %q vs %q", h1, h2)
	}

	// Content change ⇒ different hash (INV-8 / INV-7 at the file level).
	write("b.go", "package p\nvar X = 2\n")
	if h3, _ := hashFiles(dir, []string{"a.go", "b.go"}); h3 == h1 {
		t.Error("hash insensitive to content change")
	}

	// Missing file ⇒ error, never silently skipped.
	if _, err := hashFiles(dir, []string{"missing.go"}); err == nil {
		t.Error("expected error for missing file")
	}
}

func TestContribution(t *testing.T) {
	h := &Hasher{modCache: filepath.FromSlash("/gomodcache")}

	// Excluded: stdlib, pseudo-package, synthesized test main.
	for _, p := range []listPkg{
		{ImportPath: "fmt", Standard: true},
		{ImportPath: "C"},
		{ImportPath: "example/x.test", Module: &listMod{Main: true}},
	} {
		if c, err := h.contribution(p); err != nil || c != "" {
			t.Errorf("contribution(%s): got %q, %v; want \"\"", p.ImportPath, c, err)
		}
	}

	// Cache dep (classified on the package Dir under GOMODCACHE): pinned by
	// modpath@version, no file read.
	modDir := filepath.FromSlash("/gomodcache/golang.org/x/tools@v0.46.0")
	c, err := h.contribution(listPkg{
		ImportPath: "golang.org/x/tools/go/ssa",
		Dir:        filepath.Join(modDir, "go", "ssa"),
		Module:     &listMod{Path: "golang.org/x/tools", Version: "v0.46.0", Dir: modDir},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(c, "cache:") || !strings.Contains(c, "golang.org/x/tools@v0.46.0") {
		t.Errorf("cache contribution: %q", c)
	}

	// Vendored dep (Dir outside the cache, Module.Dir empty, not Main) is
	// mutable-local → hashed by content (INV-8), not pinned.
	vdir := t.TempDir()
	if err := os.WriteFile(filepath.Join(vdir, "v.go"), []byte("package v\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err = h.contribution(listPkg{
		ImportPath: "vendored/dep", Dir: vdir, GoFiles: []string{"v.go"},
		Module: &listMod{Path: "vendored/dep", Version: "v1.0.0"}, // Dir empty (vendored)
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(c, "src:vendored/dep=") {
		t.Errorf("vendored dep should hash by content, got %q", c)
	}

	// Main module: hashed by content.
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x.go"), []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err = h.contribution(listPkg{
		ImportPath: "example/p", Dir: dir, GoFiles: []string{"x.go"},
		Module: &listMod{Main: true, Dir: dir},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(c, "src:example/p=") {
		t.Errorf("src contribution: %q", c)
	}
}

func TestUnderCache(t *testing.T) {
	h := &Hasher{modCache: filepath.FromSlash("/home/u/go/pkg/mod")}
	yes := filepath.FromSlash("/home/u/go/pkg/mod/golang.org/x/tools@v0.46.0")
	if !h.underCache(yes) {
		t.Errorf("underCache(%q) = false", yes)
	}
	no := filepath.FromSlash("/home/u/repo/internal/foo")
	if h.underCache(no) {
		t.Errorf("underCache(%q) = true", no)
	}
	// Prefix-but-not-a-path-segment must not match.
	sib := filepath.FromSlash("/home/u/go/pkg/modificator")
	if h.underCache(sib) {
		t.Errorf("underCache(%q) = true (segment-boundary bug)", sib)
	}
}

// TestSourceFilesComplete pins the compiled-file-kind set: dropping a kind (a
// silent under-coverage / false-valid hole) changes the count.
func TestSourceFilesComplete(t *testing.T) {
	p := listPkg{
		GoFiles: []string{"g.go"}, CgoFiles: []string{"cg.go"}, CFiles: []string{"c.c"},
		CXXFiles: []string{"cc.cc"}, MFiles: []string{"m.m"}, HFiles: []string{"h.h"},
		FFiles: []string{"f.f"}, SFiles: []string{"s.s"}, SwigFiles: []string{"w.swig"},
		SwigCXXFiles: []string{"wc.swigcxx"}, SysoFiles: []string{"o.syso"}, EmbedFiles: []string{"e.txt"},
	}
	if got := len(p.sourceFiles()); got != 12 {
		t.Errorf("sourceFiles count: got %d, want 12 — a compiled file kind is missing", got)
	}
}

// TestParseListError: a package reporting a load Error must fail the parse, never
// be silently dropped from the closure (INV-1).
func TestParseListError(t *testing.T) {
	const stream = `{"ImportPath":"ok/pkg","Dir":"/x","Module":{"Main":true}}
{"ImportPath":"bad/pkg","Error":{"Err":"cannot find package"}}`
	if _, err := parseList(strings.NewReader(stream)); err == nil {
		t.Error("expected error when a package reports a load Error")
	}
}

// TestMaximalHashReal exercises the Tier-1 maximal pipeline against a real
// package (store, which pulls in the benchfmt cache dep), validating determinism
// and cross-module classification (a src: contribution for the main module,
// cache: for benchfmt). maximalHash is the A′ widening target (§7.3).
func TestMaximalHashReal(t *testing.T) {
	h, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	const pkg = "github.com/thegrumpylion/pew/internal/store"
	a, err := h.maximalHash(pkg)
	if err != nil {
		t.Fatalf("maximalHash: %v", err)
	}
	if len(a) != 16 {
		t.Errorf("hash len: got %d (%q)", len(a), a)
	}
	if b, _ := h.maximalHash(pkg); b != a {
		t.Errorf("hash not deterministic: %q vs %q", a, b)
	}
}

// TestComputeIncludesInitRegisteredSideEffectPackage pins INV-1 for registry
// patterns: a side-effect import's init can register an implementation that the
// benchmark later observes through package-level state and interface dispatch.
// Until declaration-level analysis proves that startup/global flow, Compute must
// use the maximal closure so that package source is hashed.
func TestComputeIncludesInitRegisteredSideEffectPackage(t *testing.T) {
	h, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	const benchPkg = "github.com/thegrumpylion/pew/internal/closure/fixtures/initregistry/bench"
	const codecPkg = "github.com/thegrumpylion/pew/internal/closure/fixtures/initregistry/codec"

	cl, err := h.Compute(benchPkg, "BenchmarkDecode")
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if len(cl.Hash) != 16 {
		t.Fatalf("hash len: got %d (%q)", len(cl.Hash), cl.Hash)
	}
	maxHash, err := h.maximalHash(benchPkg)
	if err != nil {
		t.Fatalf("maximalHash: %v", err)
	}
	if cl.Hash != maxHash {
		t.Fatalf("Compute hash %q does not use maximal closure %q", cl.Hash, maxHash)
	}

	contribs, err := h.maximalContributions(benchPkg)
	if err != nil {
		t.Fatalf("maximalContributions: %v", err)
	}
	for _, c := range contribs {
		if strings.HasPrefix(c, "src:"+codecPkg+"=") {
			return
		}
	}
	t.Fatalf("init-registered side-effect package %s missing from closure contributions: %v", codecPkg, contribs)
}

func BenchmarkHashFiles(b *testing.B) {
	dir := b.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x.go"), []byte("package p\nvar X = 1\n"), 0o644); err != nil {
		b.Fatal(err)
	}
	files := []string{"x.go"}
	for b.Loop() {
		if _, err := hashFiles(dir, files); err != nil {
			b.Fatal(err)
		}
	}
}
