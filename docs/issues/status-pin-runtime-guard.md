# `pew status --pin` and `pew stat --pin` Parity with Pinned Measurement `runtimeconfig` Guard

- **Status:** Open
- **Governing contract:** [`docs/specs/spec.md`](../specs/spec.md) (§5, §7, §9, §10.1 `REQ-pew-comparison-policies`, §12, §13 `REQ-pew-pin-derivation` / `REQ-pew-admission`), [`docs/guidance.md`](../guidance.md)
- **Lands:** awaits triage

## Problem

When `GOMAXPROCS` is unset (or empty) in the caller's environment, `pew run --pin` and `pew ab --pin` derive a topology-pinned CPU set via `derivePin` and construct a producer runtime environment with `GOMAXPROCS` set to `min(len(pin.CPUs), runtime.GOMAXPROCS(0))` (`pinEnvironment` in `cmd/pew/knobs.go`, passed to `newEngineForPkgProducer`). Because `runtimeconfig` digests explicitly-set runtime variables (`spec.md` §5, §7), that `GOMAXPROCS` width is hashed into the recording's `runtimeconfig` guard (`REQ-pew-pin-derivation`) so a pinned baseline and an unpinned run do not silently serve each other.

Neither `pew status` (`cmd/pew/status.go`) nor `pew stat`'s working-tree freshness lane (`cmd/pew/stat_analysis.go`, `cmd/pew/stat.go`, `cmd/pew/profile_compare.go`) exposes `--pin`:

1. **`pew status` false `stale (runtimeconfig)`:** `status` constructs its judging engine via `newEngineForPkg(ctx, p, env)` (with `producerEnv == nil`) and calls `explainPackageRecord` with unpinned `env`. Immediately after `pew run ./... --pin` records a clean store on a multi-core host with ambient `GOMAXPROCS` unset, `pew status ./...` reports every recorded benchmark as `stale (runtimeconfig)` unless the caller manually determines the derived pin width and prefixes `GOMAXPROCS=<width> pew status ./...`.
2. **`pew stat` false staleness warnings and `stat --fail-on-regression` exit `2`:** When `newRef == ""` (default auto `working-tree vs HEAD` and 1-ref `working-tree vs <ref>`), `statAnalysis.prepare` calls `buildEngine(..., a.env, nil, pgo)`, `stat.go` passes unpinned `env` to `explainPackageRecord`, and `compareStatProfiles` reuses that unpinned view. On a `--pin` store with ambient `GOMAXPROCS` unset, `pew stat` emits false `working-tree recording ... is stale (runtimeconfig)` warnings, `pew stat --profile` marks working-tree profiles `freshness=stale`, and `pew stat --fail-on-regression` (where omitted `--freshness` resolves to `require` per `REQ-pew-comparison-policies`) marks every obligation `eligible=false` and exits `2`.

## Resolution Scope

1. Add `--pin` to `pew status` and `pew stat` (`cmd/pew/status.go`, `cmd/pew/stat.go`, `cmd/pew/stat_analysis.go`, `cmd/pew/profile_compare.go`, `docs/guidance.md`, `docs/specs/spec.md`) reusing `derivePin`, `newEnvironments(env, pin)`, and `newEngineForPkgProducer` (and plumbing `envs.measured()` to `explainPackageRecord` for `CurrentRuntimeInputs` / `MovedRuntimeInputs`), while keeping `go list` and analysis loads unpinned at full host parallelism. Define and document `--pin` behavior on two-ref `pew stat <refA> <refB>` (where no working-tree freshness check runs).
2. Verify via integration tests in `cmd/pew` that a store recorded under `pew run --pin` (with ambient `GOMAXPROCS` unset) reports `valid` under `pew status --pin`, passes `pew stat --pin --fail-on-regression` when eligible, reports `stale (runtimeconfig)` under unpinned `pew status`, and refuses cleanly before package judging when `--pin` cannot derive a CPU set or `taskset` is unavailable.
