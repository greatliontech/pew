// Spike 1: validate the §7 closure analysis.
//
//	go run ./closure [BenchmarkName]     # explicit-flag load (no stdlib bodies)
//	ALLSYNTAX=1 go run ./closure Bench   # load syntax for all deps (stdlib bodies)
//
// Validates: loading a *test* package, finding the benchmark ssa.Function,
// CHA reachability, std vs non-std partition, interface over-approximation,
// reflect-trigger detection, and the std-callback (sort.Interface) case.
package main

import (
	"fmt"
	"go/types"
	"os"
	"sort"
	"strings"

	"golang.org/x/tools/go/callgraph"
	"golang.org/x/tools/go/callgraph/cha"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

func isStd(p string) bool {
	first := p
	if i := strings.IndexByte(p, '/'); i >= 0 {
		first = p[:i]
	}
	return !strings.Contains(first, ".")
}

func funcPkgPath(fn *ssa.Function) string {
	if fn.Pkg != nil && fn.Pkg.Pkg != nil {
		return fn.Pkg.Pkg.Path()
	}
	if o := fn.Object(); o != nil && o.Pkg() != nil {
		return o.Pkg().Path()
	}
	return ""
}

func main() {
	bench := "BenchmarkRun"
	if len(os.Args) > 1 {
		bench = os.Args[1]
	}

	mode := packages.NeedName | packages.NeedFiles | packages.NeedCompiledGoFiles |
		packages.NeedImports | packages.NeedDeps | packages.NeedTypes |
		packages.NeedSyntax | packages.NeedTypesInfo | packages.NeedModule
	if os.Getenv("ALLSYNTAX") != "" {
		mode = packages.LoadAllSyntax
		fmt.Println("(mode: LoadAllSyntax — stdlib bodies present)")
	}
	cfg := &packages.Config{Mode: mode, Tests: true}
	pkgs, err := packages.Load(cfg, "example.com/pewspikes/sample")
	if err != nil {
		fmt.Println("load error:", err)
		os.Exit(1)
	}
	if packages.PrintErrors(pkgs) > 0 {
		os.Exit(1)
	}

	fmt.Println("== loaded package variants ==")
	for _, p := range pkgs {
		fmt.Printf("  ID=%-58s name=%-8s files=%d\n", p.ID, p.Name, len(p.GoFiles))
	}

	prog, _ := ssautil.AllPackages(pkgs, ssa.InstantiateGenerics)
	prog.Build()

	var fn *ssa.Function
	var inPkg string
	for _, p := range pkgs {
		if p.Types == nil {
			continue
		}
		if obj := p.Types.Scope().Lookup(bench); obj != nil {
			if tf, ok := obj.(*types.Func); ok {
				if f := prog.FuncValue(tf); f != nil {
					fn, inPkg = f, p.ID
					break
				}
			}
		}
	}
	if fn == nil {
		fmt.Printf("\nFAIL: %s not found as an ssa.Function\n", bench)
		os.Exit(1)
	}
	fmt.Printf("\n== rooting at %s (found in %s) ==\n", bench, inPkg)

	cg := cha.CallGraph(prog)
	root := cg.Nodes[fn]
	if root == nil {
		fmt.Println("FAIL: no callgraph node for benchmark")
		os.Exit(1)
	}

	seen := map[*ssa.Function]bool{fn: true}
	q := []*callgraph.Node{root}
	for len(q) > 0 {
		n := q[0]
		q = q[1:]
		for _, e := range n.Out {
			if c := e.Callee.Func; c != nil && !seen[c] {
				seen[c] = true
				q = append(q, e.Callee)
			}
		}
	}

	var nonStd []string
	stdCount := 0
	reflectCallers := map[string]bool{}
	for f := range seen {
		pp := funcPkgPath(f)
		if pp != "" && isStd(pp) {
			stdCount++
			continue
		}
		nonStd = append(nonStd, f.String())
		for _, b := range f.Blocks {
			for _, in := range b.Instrs {
				if call, ok := in.(*ssa.Call); ok {
					if sc := call.Call.StaticCallee(); sc != nil && funcPkgPath(sc) == "reflect" {
						reflectCallers[f.String()] = true
					}
				}
			}
		}
	}
	sort.Strings(nonStd)

	fmt.Printf("\nreachable: total=%d  nonStd=%d  std=%d\n", len(seen), len(nonStd), stdCount)
	fmt.Println("\n== non-std reachable closure ==")
	for _, s := range nonStd {
		fmt.Println("  ", s)
	}

	check := func(label, sub string) {
		hit := false
		for _, s := range nonStd {
			if strings.Contains(s, sub) {
				hit = true
				break
			}
		}
		fmt.Printf("  [%-5v] %s\n", hit, label)
	}
	fmt.Println("\n== presence checks ==")
	check("formal.Greet  (interface over-approx)", "formal).Greet")
	check("casual.Greet  (interface over-approx)", "casual).Greet")
	check("byLen.Less    (std callback via sort.Interface)", "byLen).Less")
	fmt.Printf("\nreflect call-sites in non-std code: %d\n", len(reflectCallers))
	for s := range reflectCallers {
		fmt.Println("  ", s)
	}
}
