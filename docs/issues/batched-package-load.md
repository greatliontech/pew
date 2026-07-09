# Batch the whole-program SSA loads across packages

Lands: when `internal/closure` performance is next worked with the per-test-binary
partitioning below in hand, or multi-package `pew status` wall-clock proves slow

## Fault (efficiency)

`internal/closure/tier2.go` (`loadCached`/`load`) performs one
`packages.Load(LoadAllSyntax, pkgPath)` per requested package. Each load re-parses and
re-type-checks the stdlib bodies (mandatory per §7.4) and all shared dependencies from
scratch, so `pew status ./...` over N packages costs N nearly-identical whole-program
loads. (The per-*benchmark* repeats — `go list` and SSA reuse — are already amortized by
the per-package `lists`/`progs` caches; this is the residual per-*package* cost.)

## Why this is not a plain shared load (soundness)

`packages.Load` accepts multiple patterns, but the loaded `*program` is not consumed only
through per-package-keyed lookups. `tier2` roots RTA at **every package init in
`prog.prog.AllPackages()`** (tier2.go, the roots loop) — load-bearing per §7.4: an init
can register an implementation the benchmark later observes without naming the package, so
its source must be hashed. Under today's per-package load, `AllPackages()` is scoped to one
test binary. A shared `packages.Load(pkg1, pkg2, …)` builds one `ssa.Program` whose
`AllPackages()` returns **every** requested binary, so:

- Naive share: sibling binaries' inits become RTA roots → unrelated packages enter the
  closure → **hashes change**. Safe direction (over-approximation, never false-valid) but
  not the "no behavior change" a memoization promises.
- Behavior-preserving share requires scoping the init-root loop back to *this* benchmark's
  test-binary package set (group the multi-load roots by test binary via `ForTest`, compute
  each binary's package closure, iterate only that). Scope it one package too tight and a
  registering init is dropped → under-covered closure → **false-valid** (INV-1), the one
  forbidden outcome.

## Resolution

Share one `packages.Load` across the requested packages **and** partition the resulting
program per test binary so the RTA init-root set stays exactly the current per-binary set
— then prove equivalence with a differential test: for a multi-package tree, every
benchmark's `Compute` hash and unverifiable verdict must equal the per-package-load
baseline. Land only behind that proof; do not ship the naive share.
