package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/greatliontech/pew/internal/gotool"
	"github.com/greatliontech/pew/internal/store"
	"github.com/spf13/cobra"
)

func newGCCmd() *cobra.Command {
	var benchDir string
	cmd := &cobra.Command{
		Use:   "gc",
		Short: "Remove stored results for benchmarks no longer in the code",
		Long: `Remove stored benchmark results whose benchmark no longer exists.

pew gc scans ./... in the current module, enumerates top-level benchmark
declarations in package test files, and deletes matching pew recording files for
packages or benchmarks that disappeared. Build-tagged benchmark declarations are
counted as present so gc does not remove recordings for a variant just because
the current build config hides it. Files under bench-dir that do not match pew's
recording layout are ignored.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runGC(cmd.OutOrStdout(), benchDir)
		},
		Args: cobra.NoArgs,
	}
	cmd.Flags().StringVar(&benchDir, "bench-dir", "", "stored-recordings directory (default <module>/benchmarks)")
	return cmd
}

func runGC(w io.Writer, benchDir string) error {
	pkgs, err := resolvePackages([]string{"./..."})
	if err != nil {
		return err
	}
	type gcGroup struct {
		moduleDir string
		live      map[string]map[string]bool
		protected map[string]bool
	}
	groups := map[string]*gcGroup{}
	for _, p := range pkgs {
		if p.Module.Dir == "" {
			continue
		}
		dir := benchDir
		if dir == "" {
			dir = filepath.Join(p.Module.Dir, "benchmarks")
		}
		pkgRel := strings.TrimPrefix(strings.TrimPrefix(p.ImportPath, p.Module.Path), "/")
		g := groups[dir]
		if g == nil {
			g = &gcGroup{moduleDir: p.Module.Dir, live: map[string]map[string]bool{}, protected: map[string]bool{}}
			groups[dir] = g
		}
		benches, exists, err := sourceBenchmarks(p.Dir)
		if err != nil {
			fmt.Fprintf(w, "%-12s %s  (%v)\n", "error", p.ImportPath, err)
			g.protected[pkgRel] = true
			continue
		}
		if exists {
			g.live[pkgRel] = benches
		}
	}
	if len(groups) == 0 {
		moduleDir, err := currentModuleDir()
		if err != nil {
			return err
		}
		if moduleDir != "" {
			dir := benchDir
			if dir == "" {
				dir = filepath.Join(moduleDir, "benchmarks")
			}
			groups[dir] = &gcGroup{moduleDir: moduleDir, live: map[string]map[string]bool{}, protected: map[string]bool{}}
		}
	}

	var removed []string
	for dir, g := range groups {
		st := store.New(dir)
		if err := addStoreOnlySourceBenchmarks(st, g.moduleDir, g.live, g.protected); err != nil {
			return err
		}
		gone, err := gcStore(st, g.live, g.protected)
		if err != nil {
			return err
		}
		removed = append(removed, gone...)
	}
	sort.Strings(removed)
	for _, name := range removed {
		fmt.Fprintf(w, "removed      %s\n", name)
	}
	if len(removed) == 0 {
		fmt.Fprintln(w, "gc: no stale recordings")
	}
	return nil
}

func gcStore(st *store.Store, live map[string]map[string]bool, protected map[string]bool) ([]string, error) {
	recs, err := st.ListCandidates()
	if err != nil {
		return nil, err
	}
	var removed []string
	for _, r := range recs {
		parsed, err := st.Read(r.PkgRel, r.Bench, r.Label)
		if err != nil || !store.IsRecordingShape(parsed) {
			continue
		}
		if protected[r.PkgRel] {
			continue
		}
		benches, ok := live[r.PkgRel]
		if ok && benches[r.Bench] {
			continue
		}
		if err := st.Remove(r); err != nil {
			return nil, err
		}
		removed = append(removed, recordingDisplay(r))
	}
	return removed, nil
}

func addStoreOnlySourceBenchmarks(st *store.Store, moduleDir string, live map[string]map[string]bool, protected map[string]bool) error {
	recs, err := st.ListCandidates()
	if err != nil {
		return err
	}
	for _, r := range recs {
		parsed, err := st.Read(r.PkgRel, r.Bench, r.Label)
		if err != nil || !store.IsRecordingShape(parsed) {
			continue
		}
		if _, ok := live[r.PkgRel]; ok || protected[r.PkgRel] {
			continue
		}
		benches, exists, err := sourceBenchmarks(filepath.Join(moduleDir, filepath.FromSlash(r.PkgRel)))
		if err != nil {
			protected[r.PkgRel] = true
			continue
		}
		if exists {
			live[r.PkgRel] = benches
		}
	}
	return nil
}

func currentModuleDir() (string, error) {
	out, err := gotool.Run("env", "GOMOD")
	if err != nil {
		return "", err
	}
	gomod := strings.TrimSpace(string(out))
	if gomod == "" || gomod == os.DevNull {
		return "", nil
	}
	return filepath.Dir(gomod), nil
}

func sourceBenchmarks(dir string) (map[string]bool, bool, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		files = append(files, e.Name())
	}
	benches, err := benchmarksInFiles(dir, files)
	if err != nil {
		return nil, true, err
	}
	return benches, true, nil
}

func selectedBenchmarks(p pkgMeta) ([]string, error) {
	files := append([]string{}, p.TestGoFiles...)
	files = append(files, p.XTestGoFiles...)
	set, err := benchmarksInFiles(p.Dir, files)
	if err != nil {
		return nil, err
	}
	benches := make([]string, 0, len(set))
	for b := range set {
		benches = append(benches, b)
	}
	sort.Strings(benches)
	return benches, nil
}

func benchmarksInFiles(dir string, names []string) (map[string]bool, error) {
	benches := map[string]bool{}
	fset := token.NewFileSet()
	for _, name := range names {
		file, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if err != nil {
			return nil, err
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if ok && isBenchmarkDecl(fn) {
				benches[fn.Name.Name] = true
			}
		}
	}
	return benches, nil
}

func isBenchmarkDecl(fn *ast.FuncDecl) bool {
	if fn.Recv != nil || !isBenchmarkName(fn.Name.Name) || fn.Type.Params == nil || fn.Type.Params.NumFields() != 1 {
		return false
	}
	if fn.Type.TypeParams != nil && fn.Type.TypeParams.NumFields() != 0 {
		return false
	}
	if fn.Type.Results != nil && fn.Type.Results.NumFields() != 0 {
		return false
	}
	if len(fn.Type.Params.List[0].Names) > 1 {
		return false
	}
	star, ok := fn.Type.Params.List[0].Type.(*ast.StarExpr)
	if !ok {
		return false
	}
	return isGoTestBenchmarkParam(star.X)
}

func isBenchmarkName(name string) bool {
	const prefix = "Benchmark"
	if !strings.HasPrefix(name, prefix) {
		return false
	}
	suffix := name[len(prefix):]
	if suffix == "" {
		return true
	}
	r, _ := utf8.DecodeRuneInString(suffix)
	return !unicode.IsLower(r)
}

func isGoTestBenchmarkParam(expr ast.Expr) bool {
	switch x := expr.(type) {
	case *ast.SelectorExpr:
		return x.Sel.Name == "B"
	case *ast.Ident:
		return x.Name == "B"
	default:
		return false
	}
}

func recordingDisplay(r store.Recording) string {
	name := r.Bench
	if r.Label != "" {
		name += "." + r.Label
	}
	if r.PkgRel == "" {
		return name
	}
	return r.PkgRel + "." + name
}
