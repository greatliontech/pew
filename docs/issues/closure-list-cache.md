# Cache `go list -json -deps -test` per package in the closure hasher

Lands: when `internal/closure` performance is next worked, or `pew status` wall-clock proves slow

## Fault (efficiency)

`internal/closure/tier2.go` (`tier2`) calls `h.list(pkgPath)` — a `go list -json -deps -test`
subprocess plus a full JSON decode of the dependency graph — on **every** `Compute` call, i.e. once
per benchmark. `maximalHash` runs the same listing again when a blind spot widens to Tier-1. A
package with 10 benchmarks pays 10+ identical subprocess runs, each typically hundreds of
milliseconds, dominating everything except the (already-cached) SSA load.

## Resolution

Memoize the parsed `[]listPkg` per package path on `Hasher`, exactly as `loadCached` memoizes the
SSA program. The listing is keyed by the same immutable inputs as the SSA cache within one process
invocation, so no staleness concern arises (both caches live only for a single `pew` run).
