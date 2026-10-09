# Issue docs — deferred follow-ups

Tracked deferrals carrying a `Lands:` trigger. On resolution, the load-bearing rationale is promoted
inline to the spec / a test, and the doc is deleted (git holds history) — per the Issue triage
close-out convention.

| slug | summary | Lands |
|------|---------|-------|
| [observation-premise-property-evidence](observation-premise-property-evidence.md) | existing premise examples do not establish the invariant's required property-evidence class | performance-evidence plan chunk 12, before its final verification gate |
| [train-riders-for-the-performance-evidence-plan](train-riders-for-the-performance-evidence-plan.md) | remaining final-workflow riders and cross-tool coordination; residual dependency bump tracked by the train | performance-evidence plan chunk 12 |
| [mutation-oracle-observation](mutation-oracle-observation.md) | incomplete campaigns and blocked chunk 11 campaign scope, legitimate unsupported cwd/evidence, and unsound negative subprocess coverage require shared-tool resolution and sound remeasurement | performance-evidence plan chunk 12 final mutation gate |
| [guidance-purpose-column-second-enumeration](guidance-purpose-column-second-enumeration.md) | §12's opt-in purpose column restates the document's knob prose in its own words, pinned to nothing | performance-evidence plan chunk 12.2 (audit 335) |
| [remote-bench-execution](remote-bench-execution.md) | run measurements on a dedicated homelab bench machine: gRPC-over-SSH `pew agent`, machine lease, off-box builds, calibration drift-vet | capability charter (gofresh docs/plans/capability-charters.md) — activates when a dedicated bench machine is provisioned |
| [per-arm-noise-floors](per-arm-noise-floors.md) | one global `--threshold` vs ±10% layout-only cross-commit drift on ns-class arms (run-to-run CV measured 0.2–1.4%); false regressions get hand-inspected — derive per-arm floors from lineage history | performance-evidence plan chunk 12 |
| [derived-state-recompute-invariance-witness](derived-state-recompute-invariance-witness.md) | `REQ-pew-derived-state`'s recompute/discard-invariance clause has no pew-side witness (engine-owned path; the bound tests pin only the strategy-break authority arm; a gap cannot express a per-clause shortfall) | cross-tool train chunk 232 |
| [spec-wide-requirement-forming](spec-wide-requirement-forming.md) | only §13 is REQ-formed; a test witnessing a §§1–12 contract has no id to bind against — convert sections on demand, same structure-only discipline | the home rule at train 232r; the per-section conversions on demand in each performance-evidence chunk (audit 335) |
| [mcp-surface](mcp-surface.md) | pew serves the CLI only; whether an LLM reader is owed an MCP surface (or `run`/`ab`/`gc` a `--json`) is a product-scope call | performance-evidence chunk 12 for remaining machine-readable/progress triage; MCP when a driving agent appears |

## In-spec upgrade paths (tracked inline, not here)

Several design alternatives are documented at their spec sites as "upgrade paths on measured need."
They are kept current with the spec, so they need no separate tracking doc — listed here only as an
index:

- VTA call graph, if RTA ever over-includes (§7.4)
- escape-rule to skip loading stdlib bodies (§7.4)
- per-declaration hashing *into* cache deps (§7.7)
- gitignored persistent closure memo (§6)
- same-identity sample merge (§6)
