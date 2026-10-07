# Issue docs — deferred follow-ups

Tracked deferrals carrying a `Lands:` trigger. On resolution, the load-bearing rationale is promoted
inline to the spec / a test, and the doc is deleted (git holds history) — per the Issue triage
close-out convention.

| slug | summary | Lands |
|------|---------|-------|
| [fingerprint-encoding-ownership](fingerprint-encoding-ownership.md) | pew's parallel fingerprint encoding conflicts with the shared native-record contract; reconcile the artifact boundary before adding observation evidence | performance-evidence plan chunk 7 |
| [train-riders-for-the-performance-evidence-plan](train-riders-for-the-performance-evidence-plan.md) | remaining cross-tool riders for freshness, comparisons, profiles and A/B; residual dependency bump tracked by the train | performance-evidence plan chunks 7–12 as named inside |
| [ab-control-process-cancellation](ab-control-process-cancellation.md) | native Git preparation/cleanup can ignore A/B cancellation and cleanup cannot report its failure | performance-evidence plan chunk 11 |
| [mutation-oracle-observation](mutation-oracle-observation.md) | changed-code measurement banks kills but cwd-sensitive integration observations are non-reusable and final evidence work reaches its deadline | performance-evidence plan chunk 8 |
| [guidance-purpose-column-second-enumeration](guidance-purpose-column-second-enumeration.md) | §12's opt-in purpose column restates the document's knob prose in its own words, pinned to nothing | cross-tool train chunk 232 |
| [remote-bench-execution](remote-bench-execution.md) | run measurements on a dedicated homelab bench machine: gRPC-over-SSH `pew agent`, machine lease, off-box builds, calibration drift-vet | capability charter (gofresh docs/plans/capability-charters.md) — activates when a dedicated bench machine is provisioned |
| [profile-capture-attribution](profile-capture-attribution.md) | subject attribution is consumer hand protocol: capture profiles with recordings, attribution verdict in status, profile diff in stat | performance-evidence plan chunks 10/11 |
| [per-arm-noise-floors](per-arm-noise-floors.md) | one global `--threshold` vs ±10% layout-only cross-commit drift on ns-class arms (run-to-run CV measured 0.2–1.4%); false regressions get hand-inspected — derive per-arm floors from lineage history | performance-evidence plan chunk 12 |
| [audit-note-renderers-one-grammar](audit-note-renderers-one-grammar.md) | compare renders an audit row's difference two ways (run conditions vs every other audit row): one renderer with a per-row comparison | performance-evidence plan chunk 9 |
| [observed-fingerprint-recording-path](observed-fingerprint-recording-path.md) | observed reuse needs supported outcome evidence, canonical recording and an explicit per-arm capture/check path | performance-evidence plan chunk 8 |
| [derived-state-recompute-invariance-witness](derived-state-recompute-invariance-witness.md) | `REQ-pew-derived-state`'s recompute/discard-invariance clause has no pew-side witness (engine-owned path; the bound tests pin only the strategy-break authority arm; a gap cannot express a per-clause shortfall) | cross-tool train chunk 232 |
| [spec-wide-requirement-forming](spec-wide-requirement-forming.md) | only §13 is REQ-formed; a test witnessing a §§1–12 contract has no id to bind against — convert sections on demand, same structure-only discipline | each performance-evidence chunk, its section; the home rule at the train's 232 residue |
| [ab-out-multi-package](ab-out-multi-package.md) | `ab --out` over several packages keeps only the last package's artifact; a per-package path or a multi-section format is a contract change | performance-evidence plan chunk 11 |
| [mcp-surface](mcp-surface.md) | pew serves the CLI only; whether an LLM reader is owed an MCP surface (or `run`/`ab`/`gc` a `--json`) is a product-scope call | performance-evidence chunks 9 and 12 for JSON/progress; MCP when a driving agent appears |
| [verdict-path-consolidation](verdict-path-consolidation.md) | the vouch set is assembled from four process-wide globals and admission decodes each row three times; collapse to one resolver value and one row decode | performance-evidence plan chunk 8 |
| [strategy-stale-arms-flipped-valid-without-rerecord](strategy-stale-arms-flipped-valid-without-rerecord.md) | historical strategy-verdict disagreement needs exact recorded/derived keys and binary identity to diagnose | performance-evidence plan chunk 8 |

## In-spec upgrade paths (tracked inline, not here)

Several design alternatives are documented at their spec sites as "upgrade paths on measured need."
They are kept current with the spec, so they need no separate tracking doc — listed here only as an
index:

- VTA call graph, if RTA ever over-includes (§7.4)
- escape-rule to skip loading stdlib bodies (§7.4)
- per-declaration hashing *into* cache deps (§7.7)
- gitignored persistent closure memo (§6)
- same-identity sample merge (§6)
