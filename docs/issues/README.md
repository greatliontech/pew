# Issue docs — deferred follow-ups

Tracked deferrals carrying a `Lands:` trigger. On resolution, the load-bearing rationale is promoted
inline to the spec / a test, and the doc is deleted (git holds history) — per the Issue triage
close-out convention.

| issue | summary | Lands |
|---|---|---|
| [stipulator-policy-and-test-cache-isolation](stipulator-policy-and-test-cache-isolation.md) | `cmd/pew` tests read/write ambient `~/.cache/gofresh` (`272` uncacheable witnesses), and `pew` lacks a `.stipulator/policy.textproto` splitting `cmd/pew` (`race:core`) from fast `internal/...` leaf packages (`race:leaf`) and declaring reviewed `bracket_paths` (`/usr/bin/git`, `internal`). | Chunk 1 in [`docs/plans/plan.md`](../plans/plan.md) |
| [shared-cli-mcp-process-utilities](shared-cli-mcp-process-utilities.md) | `internal/guidance`, `internal/toolversion`, `internal/gotool`, `internal/shortgates`, `internal/shapecorpus`, and `internal/trace` are internalized across `pew`, `stipulator`, and `gomutant` after `gofresh` single-package purification; extract into a dedicated shared CLI/MCP/process support module when triggered. | User-authorized deferral until triggered during cross-tool CLI/MCP substrate consolidation |
| [status-pin-runtime-guard](status-pin-runtime-guard.md) | `pew status` and `pew stat` lack `--pin` and evaluate working-tree `runtimeconfig` without `pinEnvironment`'s `GOMAXPROCS` width when ambient `GOMAXPROCS` is unset, causing false `stale (runtimeconfig)` verdicts and `stat --fail-on-regression` exit `2` on `pew run --pin` stores. | awaits triage |
| [status-and-stat-bench-and-profile-top-filters](status-and-stat-bench-and-profile-top-filters.md) | `pew status` and `pew stat` lack `--bench` selection (and `stat` lacks package filtering), and `status --explain`, `stat --profile`, and `ab --profile` lack `--top` hotspot ranking/truncation. | awaits triage |
| [single-recording-show-verb](single-recording-show-verb.md) | `pew` has no single-recording summary command (`pew show`) to display a stored baseline's sample medians/spread (`sec/op`, `B/op`, `allocs/op`, custom `ReportMetric`s) and top hotspots without a two-side comparison or reading raw `.txt` files. | awaits triage |
| [profile-source-line-attribution](profile-source-line-attribution.md) | Companion profile indexes already store verified mutable `sources` snapshots (`REQ-pew-profile-report`), but `pew` only reports function-level weights with no hermetic per-line (`pprof -list`-style) source hotspot view. | awaits triage |
| [profile-setup-vs-timed-loop-attribution](profile-setup-vs-timed-loop-attribution.md) | Whole-process `alloc` and `cpu` profiles mix pre-`b.ResetTimer()` / `init` fixture construction with timed-loop execution, making `0 allocs/op` benchmarks appear allocation-heavy without call-site phase tagging. | awaits triage |
| [contained-child-orphan-on-sigkill](contained-child-orphan-on-sigkill.md) | `internal/gotool` spawns contained children (`pew-testbin-*`) in a separate process group (`Setpgid: true`) without Linux `Pdeathsig: SIGKILL` (and Go disarms `-test.timeout` before `runBenchmarks`), so `SIGKILL`ing `pew`'s process group orphans `pew-testbin-*` under PID 1 holding inherited `flock` descriptors (`~/.machine.lock`). | awaits triage |

## In-spec upgrade paths (tracked inline, not here)

These five alternatives remain conditional obligations, not automatic mechanism
choices:

- VTA call graph (§7.4) — only after a demonstrated RTA precision
  shortfall and a witness separating relevant from spurious reachability.
- Skipping standard-library body loads (§7.4) — conditioned on measured cost
  and the escape/soundness proof.
- Per-declaration hashing into cache dependencies (§7.7) —
  conditioned on relevant-edit invalidation and irrelevant-edit reuse evidence.
- Persistent closure memo (§6) — owned by the shared derivation
  layer under complete generation identities, never a second Pew store.
- Same-identity sample merge (§6) — only for a real augmentation
  workflow after lineage, conditions and sample-independence obligations are
  established; equal identity alone licenses no merge.
