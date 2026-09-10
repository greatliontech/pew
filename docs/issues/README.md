# Issue docs — deferred follow-ups

Tracked deferrals carrying a `Lands:` trigger. On resolution, the load-bearing rationale is promoted
inline to the spec / a test, and the doc is deleted (git holds history) — per the Issue triage
close-out convention.

| slug | summary | Lands |
|------|---------|-------|
| [remote-bench-execution](remote-bench-execution.md) | run measurements on a dedicated homelab bench machine: gRPC-over-SSH `pew agent`, machine lease, off-box builds, calibration drift-vet | capability charter (gofresh docs/plans/capability-charters.md) — activates when a dedicated bench machine is provisioned |
| [profile-capture-attribution](profile-capture-attribution.md) | subject attribution is consumer hand protocol: capture profiles with recordings, attribution verdict in status, profile diff in stat | cross-tool train chunk 15 |
| [per-arm-noise-floors](per-arm-noise-floors.md) | one global `--threshold` vs ±10% layout-only cross-commit drift on ns-class arms (run-to-run CV measured 0.2–1.4%); false regressions get hand-inspected — derive per-arm floors from lineage history | cross-tool train chunk 15 (keys on chunk 102's sliced closures) |
| [observed-fingerprint-recording-path](observed-fingerprint-recording-path.md) | plain-Capture recordings leave every true-external-effect benchmark permanently unverifiable; adopt CaptureObserved per arm and retire §7.8's no-proof sentence | cross-tool train chunk 127 (opens with a design discussion) |
| [derived-state-recompute-invariance-witness](derived-state-recompute-invariance-witness.md) | `REQ-pew-derived-state`'s recompute/discard-invariance clause has no pew-side witness (engine-owned path; the bound tests pin only the strategy-break authority arm; a gap cannot express a per-clause shortfall) | cross-tool train chunk 232 |
| [spec-wide-requirement-forming](spec-wide-requirement-forming.md) | only §13 is REQ-formed; a test witnessing a §§1–12 contract has no id to bind against — convert sections on demand, same structure-only discipline | when a binding needs to claim a §§1–12 contract that carries no REQ id |
| [ab-out-multi-package](ab-out-multi-package.md) | `ab --out` over several packages keeps only the last package's artifact; a per-package path or a multi-section format is a contract change | cross-tool train chunk 174 |
| [mcp-surface](mcp-surface.md) | pew serves the CLI only; whether an LLM reader is owed an MCP surface (or `run`/`ab`/`gc` a `--json`) is a product-scope call | cross-tool train chunk 177 for the JSON and progress step; the MCP surface is deferred 2026-09-07 (user decision: revisit when a driving agent appears) |
| [guidance-knobs-and-verb-table](guidance-knobs-and-verb-table.md) | §12's verb defaults table and the guidance knob blocks enumerate one contract twice; generating one from the other changes the fleet guidance format | cross-tool train chunk 173 |
| [verdict-path-consolidation](verdict-path-consolidation.md) | the vouch set is assembled from four process-wide globals and admission decodes each row three times; collapse to one resolver value and one row decode | cross-tool train chunk 174 |
| [audit-note-keys-mirror-the-spec](audit-note-keys-mirror-the-spec.md) | compare's audit-note keys and §5's audit rows are two hand-kept lists; one mirror pin or one registry mark | cross-tool train chunk 232 |

## In-spec upgrade paths (tracked inline, not here)

Several design alternatives are documented at their spec sites as "upgrade paths on measured need."
They are kept current with the spec, so they need no separate tracking doc — listed here only as an
index:

- VTA call graph, if RTA ever over-includes (§7.4)
- escape-rule to skip loading stdlib bodies (§7.4)
- per-declaration hashing *into* cache deps (§7.7)
- gitignored persistent closure memo (§6)
- same-identity sample merge (§6)
