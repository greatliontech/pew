# Issue docs — deferred follow-ups

Tracked deferrals carrying a `Lands:` trigger. On resolution, the load-bearing rationale is promoted
inline to the spec / a test, and the doc is deleted (git holds history) — per the Issue triage
close-out convention.

| issue | summary | Lands |
|---|---|---|
| [ab-memoization-and-cli-helper-deduplication](ab-memoization-and-cli-helper-deduplication.md) | Switch `sideGuards` in `cmd/pew/ab.go` to `preparationReader`, eliminate duplicate `sideGuards` and `abPackageMetadata` calls per package in `pew ab`, and deduplicate `run.HarnessCompleted` and `resolveExistingPrefix` in `cmd/pew`. | Chunk 6 (`gofresh/docs/plans/plan.md`) |
| [shared-cli-mcp-process-utilities](shared-cli-mcp-process-utilities.md) | `internal/guidance`, `internal/toolversion`, `internal/gotool`, `internal/shortgates`, `internal/shapecorpus`, and `internal/trace` are internalized across `pew`, `stipulator`, and `gomutant` after `gofresh` single-package purification; extract into a dedicated shared CLI/MCP/process support module when triggered. | User-authorized deferral until triggered during cross-tool CLI/MCP substrate consolidation |

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
