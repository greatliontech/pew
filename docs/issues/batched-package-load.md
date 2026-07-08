# Batch the whole-program SSA loads across packages

Lands: when `internal/closure` performance is next worked, or multi-package `pew status` wall-clock
proves slow

## Fault (efficiency)

`internal/closure/tier2.go` (`loadCached`/`load`) performs one
`packages.Load(LoadAllSyntax, pkgPath)` per requested package. Each load re-parses and re-type-checks
the stdlib bodies (mandatory per §7.4) and all shared dependencies from scratch, so
`pew status ./...` over N packages costs N nearly-identical whole-program loads — the dominant cost
of the command, multiplied by package count.

## Resolution

`packages.Load` accepts multiple patterns in one call: load all requested packages together, build
one shared SSA program, and index per-package `program` views (benchmark roots, TestMain) from the
single load. The per-package `program` interface and the analyzer stay unchanged; only the load is
shared. Verify the shared load preserves per-test-binary identity where it matters (a package's
`.test` main and `_test` variants are distinct `packages.Package` values per `ForTest`, which the
existing `benchmarkRootPackage` matching already distinguishes).
