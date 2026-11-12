# `cmd/pew` Test Cache Isolation & Core/Leaf `.stipulator/policy.textproto` Configuration

- **Status:** Open
- **Governing contract:** [`docs/specs/spec.md`](../specs/spec.md), `.stipulator/manifest.textproto`
- **Lands:** Chunk 1 in [`docs/plans/plan.md`](../plans/plan.md)

## Problem

Full unscoped `stipulator check` evaluation on `pew` (`555` bindings, `480` witness executions across `13` packages) leaves `465/480` witnesses uncacheable and throttles `cmd/pew` execution (`17m08s` cold, `10m21s` warm) for two `pew`-side reasons alongside upstream `gofresh` and `stipulator` issues:

1. **Unisolated Ambient `XDG_CACHE_HOME` in `cmd/pew` Integration Tests (`272` Uncacheable Witnesses)**
   - Integration tests in `cmd/pew` invoke `pew` (which drives `gofresh`) without isolating `XDG_CACHE_HOME` to a per-test temporary directory.
   - Child runs read and mutate `5,950` paths under `/home/nikolas/.cache/gofresh`, sealing `272` `cmd/pew` witnesses unverifiable (`external directory input: /home/nikolas/.cache/gofresh`) and competing with the outer `stipulator check` engine's own cache writes and `2 GiB` cache prunes.

2. **Missing `.stipulator/policy.textproto` (`GOMAXPROCS=2` Throttling & Unbracketed `/usr/bin/git` / `internal` Reads)**
   - Without `.stipulator/policy.textproto`, `stipulator check` runs all `13` packages in a single default invocation (`SpawnBound = 13` -> `GOMAXPROCS = 32 / 13 = 2`), starving `cmd/pew` (`9m47s` binary runtime) after the 12 small packages finish in seconds.
   - Reads of `/usr/bin/git` (`17` witnesses) and tree-relative `internal` (`11` witnesses) fail post-run bracket coverage (`runtime input not covered by observation bracket`) because no reviewed `bracket_paths` are declared.

## Resolution Scope

1. Because over 150 tests across 38 `*_test.go` files in `cmd/pew` call `t.Parallel()` (where per-test `t.Setenv` panics), isolate `cmd/pew` via a suite-level `TestMain(m *testing.M)` (delegating to an internal test-environment helper matching `gomutant/internal/testsuite.Main` that resolves and pins `GOCACHE` via `gotool.GoBuildCache` and sets `XDG_CACHE_HOME` to a suite-owned `os.MkdirTemp("", "pew-test-cache-*")` directory removed after `m.Run()`), plus extract one `isolateCacheHome(t)` helper replacing the duplicated `gotool.GoBuildCache` setup in `cmd/pew/derived_state_test.go` (`lines 31–36`) and `cmd/pew/vouch_test.go` (`lines 57–62`).
2. Add `.stipulator/policy.textproto` partitioning:
   - `race:core` (`./cmd/pew` at wide `GOMAXPROCS`, with reviewed `assume_pure: true` + `bracket_paths` `/bin/sh`, `/usr/bin/sh`, `/usr/bin/git`, `internal` — matching `gofresh/.stipulator/policy.textproto` because `cmd/pew`'s `TestMain` `os.Setenv`/`os.MkdirTemp` setup and `go`/`git` subprocess tests are genuine external blindspots under `testMainObservedEffects` / `os/exec` while bracketed `RuntimeInputs` and `SharedDynamicStateCause` guards remain enforced), and
   - `race:leaf` (`.` and pure `./internal/...` packages **without** `assume_pure` and without `TestMain` `os.Setenv`, so `gofresh`'s static observability and Tier-2 effect narrowing remain enforced end-to-end on pure library packages, splitting or scoping subprocess-spawning `internal/run` / `internal/git` witnesses only where genuine `os/exec` subprocess blindspots exist).
3. Re-verify `stipulator check` after upstream `gofresh` (`math` auditset, `fmt.Fprintf` `*tabwriter.Writer` narrowing, `internal/shortgates` `go/ast.Stmt.Pos`, `internal/guidance` local closure parameter propagation, `internal/toolversion` `ReadBuildInfo` audit, widened closure memos) and `stipulator` (`stipulator-retained-*` exclusion, per-attempt `ScratchRoot`) fixes land, explicitly dispositioning remaining `internal/trace` (`2` subjects dispatching `context.Context.Value` / `Sink.Emit`), third-party state (`openpgp`, `pprof`, `x/perf/benchproc` `maphash.MakeSeed`), and expected-uncacheable `testing.Setenv` (`5` subjects) / `golang.org/x/sys/unix` cases.
