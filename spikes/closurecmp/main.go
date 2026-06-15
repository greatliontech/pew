// Spike 1b: compare CHA vs RTA vs VTA precision for the closure rooted at a
// single benchmark. CHA over-approximates dynamic dispatch program-wide; this
// measures whether a flow-sensitive graph (RTA/VTA) recovers a tight closure.
//
//	go run ./closurecmp [BenchmarkName]
package main

import (
	"fmt"
	"go/types"
	"os"
	"sort"
	"strings"

	"golang.org/x/tools/go/callgraph"
	"golang.org/x/tools/go/callgraph/cha"
	"golang.org/x/tools/go/callgraph/rta"
	"golang.org/x/tools/go/callgraph/vta"
	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

const modPrefix = "example.com/pewspikes/"

// funcPkgPath walks parents so closures/instantiations classify by their
// enclosing package (fixes the misclassification seen in spike 1).
func funcPkgPath(fn *ssa.Function) string {
	for fn != nil {
		if fn.Pkg != nil && fn.Pkg.Pkg != nil {
			return fn.Pkg.Pkg.Path()
		}
		if o := fn.Object(); o != nil && o.Pkg() != nil {
			return o.Pkg().Path()
		}
		fn = fn.Parent()
	}
	return ""
}

func isStd(p string) bool {
	first := p
	if i := strings.IndexByte(p, '/'); i >= 0 {
		first = p[:i]
	}
	return p != "" && !strings.Contains(first, ".")
}

func reach(cg *callgraph.Graph, root *ssa.Function) map[*ssa.Function]bool {
	seen := map[*ssa.Function]bool{root: true}
	n := cg.Nodes[root]
	if n == nil {
		return seen
	}
	q := []*callgraph.Node{n}
	for len(q) > 0 {
		cur := q[0]
		q = q[1:]
		for _, e := range cur.Out {
			if c := e.Callee.Func; c != nil && !seen[c] {
				seen[c] = true
				q = append(q, cg.Nodes[c])
			}
		}
	}
	return seen
}

func summarize(label string, seen map[*ssa.Function]bool) {
	nonStd := 0
	own := map[string]bool{}
	for f := range seen {
		pp := funcPkgPath(f)
		if isStd(pp) {
			continue
		}
		nonStd++
		if strings.HasPrefix(pp, modPrefix) {
			own[f.String()] = true
		}
	}
	keys := make([]string, 0, len(own))
	for k := range own {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Printf("\n=== %s ===\n total=%d  nonStd=%d  ownModule=%d\n", label, len(seen), nonStd, len(own))
	for _, k := range keys {
		fmt.Println("   ", k)
	}
}

func main() {
	bench := "BenchmarkRun"
	if len(os.Args) > 1 {
		bench = os.Args[1]
	}
	cfg := &packages.Config{Mode: packages.LoadAllSyntax, Tests: true}
	pkgs, err := packages.Load(cfg, "example.com/pewspikes/sample")
	if err != nil || packages.PrintErrors(pkgs) > 0 {
		fmt.Println("load error:", err)
		os.Exit(1)
	}
	prog, _ := ssautil.AllPackages(pkgs, ssa.InstantiateGenerics)
	prog.Build()

	var root *ssa.Function
	for _, p := range pkgs {
		if p.Types == nil {
			continue
		}
		if obj := p.Types.Scope().Lookup(bench); obj != nil {
			if tf, ok := obj.(*types.Func); ok {
				if f := prog.FuncValue(tf); f != nil {
					root = f
					break
				}
			}
		}
	}
	if root == nil {
		fmt.Println("benchmark not found:", bench)
		os.Exit(1)
	}
	fmt.Printf("rooted at %s\n", bench)

	chaG := cha.CallGraph(prog)
	summarize("CHA", reach(chaG, root))

	rtaRes := rta.Analyze([]*ssa.Function{root}, true)
	summarize("RTA (root=bench)", reach(rtaRes.CallGraph, root))

	vtaG := vta.CallGraph(ssautil.AllFunctions(prog), chaG)
	summarize("VTA (1 pass over CHA)", reach(vtaG, root))

	vtaG2 := vta.CallGraph(ssautil.AllFunctions(prog), vtaG)
	summarize("VTA (2 passes)", reach(vtaG2, root))
}
