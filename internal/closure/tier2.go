package closure

import (
	"fmt"
	"go/types"
	"strings"

	"golang.org/x/tools/go/packages"
	"golang.org/x/tools/go/ssa"
	"golang.org/x/tools/go/ssa/ssautil"
)

// program is the loaded whole-program SSA for one package's test binary, cached
// so per-benchmark Compute calls amortize the dominant load cost (§7.4).
type program struct {
	prog  *ssa.Program
	pkgs  []*packages.Package
	roots map[string]*ssa.Function // benchmark function name → its SSA function
}

// loadCached loads (once) and returns the whole-program SSA for pkgPath.
func (h *Hasher) loadCached(pkgPath string) (*program, error) {
	if p, ok := h.progs[pkgPath]; ok {
		return p, nil
	}
	p, err := load(pkgPath)
	if err != nil {
		return nil, err
	}
	h.progs[pkgPath] = p
	return p, nil
}

// load builds whole-program SSA for pkgPath's test binary: all-dependency syntax
// (stdlib bodies included, §7.4) with generics instantiated, so RTA can traverse
// real edges through std and dispatch generic instantiations concretely. A load
// error is fatal — analyzing a partial program could miss reachable code and
// report a stale result valid (INV-1).
func load(pkgPath string) (*program, error) {
	cfg := &packages.Config{Mode: packages.LoadAllSyntax, Tests: true}
	pkgs, err := packages.Load(cfg, pkgPath)
	if err != nil {
		return nil, fmt.Errorf("closure: load %s: %w", pkgPath, err)
	}
	var errs []string
	packages.Visit(pkgs, nil, func(p *packages.Package) {
		for _, e := range p.Errors {
			errs = append(errs, e.Error())
		}
	})
	if len(errs) > 0 {
		return nil, fmt.Errorf("closure: load %s: %s", pkgPath, strings.Join(errs, "; "))
	}

	prog, _ := ssautil.AllPackages(pkgs, ssa.InstantiateGenerics)
	prog.Build()

	roots := map[string]*ssa.Function{}
	for _, p := range pkgs {
		if p.Types == nil {
			continue
		}
		scope := p.Types.Scope()
		for _, name := range scope.Names() {
			if !strings.HasPrefix(name, "Benchmark") {
				continue
			}
			fn, ok := scope.Lookup(name).(*types.Func)
			if !ok {
				continue
			}
			if f := prog.FuncValue(fn); f != nil {
				roots[name] = f
			}
		}
	}
	return &program{prog: prog, pkgs: pkgs, roots: roots}, nil
}

// Compute returns the closure for one benchmark of pkgPath (spec §7).
//
// Current stage (7.2): the whole-program SSA is loaded and the benchmark root is
// resolved (validating the benchmark exists), but the hash remains the maximal
// Tier-1 closure — sound by construction (INV-1). Declaration-level precision
// must first prove startup/global side-effect coverage (§7.1, §7.3-A′); a graph
// rooted only at the benchmark is not sound for init-registered implementations.
func (h *Hasher) Compute(pkgPath, bench string) (Closure, error) {
	prog, err := h.loadCached(pkgPath)
	if err != nil {
		return Closure{}, err
	}
	root := prog.roots[bench]
	if root == nil {
		return Closure{}, fmt.Errorf("closure: benchmark %s not found in %s", bench, pkgPath)
	}
	_ = root // Root resolution validates that the requested benchmark exists.

	hash, err := h.maximalHash(pkgPath)
	if err != nil {
		return Closure{}, err
	}
	return Closure{Hash: hash}, nil
}
