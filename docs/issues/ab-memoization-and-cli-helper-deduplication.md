# Memoize `sideGuards` / `abPackageMetadata` in `cmd/pew/ab.go` and Deduplicate CLI Helpers

**Lands:** Chunk 8 in `gofresh/docs/plans/plan.md` (`Downstream Fleet Refactoring, Batching & Startup Purity`).

## Context & Concrete Findings

### 1. Unmemoized `gotool.Reader` and Duplicate `sideGuards` Calls in `cmd/pew/ab.go` (`cmd/pew/ab.go:66, 405, 434, 438, 509, 513`, `cmd/pew/ab_profiles.go:163`)

1. `cmd/pew/ab.go:66` (`sideGuards`) calls `reader := gotool.Reader(moduleDir, env, dependencies(ctx).prepare)`, constructing a fresh, unmemoized `EnvReader` on every call instead of calling `preparationReader(ctx, moduleDir, env)` (`cmd/pew/provenance.go:28`, backed by `inv.readers` `gotool.RunMemo`).
2. `prepareABPackage` calls `ac.sideGuards` **twice per side per package** with identical arguments:
   - First at `cmd/pew/ab.go:434, 438` (`guardsA, err := ac.sideGuards(ctx, sideAPkgDir, ac.envA)` and `guardsB, err := ac.sideGuards(ctx, sideBPkgDir, ac.envB)`).
   - Second at `cmd/pew/ab.go:509, 513` (`prep.guardsA, err = ac.sideGuards(ctx, sideAPkgDir, ac.envA)` and `prep.guardsB, err = ac.sideGuards(ctx, sideBPkgDir, ac.envB)`).

### 2. Duplicate `abPackageMetadata` (`go list -json .`) Calls in `cmd/pew/ab.go` (`cmd/pew/ab.go:298, 391`)

- When `--out` is set (`output != nil`), `runAB` calls `abPackageMetadata(ctx, dir, env)` (`gotool.List(ctx, dir, env, "-json", ".")`) for every package at `cmd/pew/ab.go:298` to preflight module roots, and `prepareABPackage` immediately calls `abPackageMetadata(ctx, sideBPkgDir, env)` again on the same directory at `cmd/pew/ab.go:391`.

### 3. Duplicate `PASS` Scanner & Symlink Prefix Resolution in `cmd/pew` (`cmd/pew/profiles.go:484-492`, `cmd/pew/run.go:371-393`)

- `cmd/pew/profiles.go:484-492` (`diagnosticChildren`) manually splits `out` on `'\n'` and scans for `"PASS"` instead of calling `run.HarnessCompleted(out)` (`internal/run/run.go:55-62`).
- `cmd/pew/run.go:371-393` (`moduleBenchDir`) manually walks parent directories calling `filepath.EvalSymlinks`, duplicating `resolveExistingPrefix` in `cmd/pew/runtime_destinations.go`.

## Resolution

During Chunk 8:
1. Switch `sideGuards` (`cmd/pew/ab.go:66`) to `preparationReader(ctx, moduleDir, env)` and reuse `guardsA` / `guardsB` across `prepareABPackage` (`ab.go:434-515`).
2. Memoize or thread `abPackageMetadata` in `cmd/pew/ab.go` so `go list -json .` runs at most once per `(dir, env)`.
3. Replace the open-coded `"PASS"` loop in `cmd/pew/profiles.go` with `run.HarnessCompleted(out)` and reuse `resolveExistingPrefix` in `moduleBenchDir`.
