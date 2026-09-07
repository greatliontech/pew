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
| [derived-state-recompute-invariance-witness](derived-state-recompute-invariance-witness.md) | `REQ-pew-derived-state`'s recompute/discard-invariance clause has no pew-side witness (engine-owned path; the bound tests pin only the strategy-break authority arm; a gap cannot express a per-clause shortfall) | when a pew-side integration anchor exercising the gofresh engine's derived-state discard/recompute path lands in the test surface |
| [spec-wide-requirement-forming](spec-wide-requirement-forming.md) | only §13 is REQ-formed; a test witnessing a §§1–12 contract has no id to bind against — convert sections on demand, same structure-only discipline | when a binding needs to claim a §§1–12 contract that carries no REQ id |
| [ab-out-multi-package](ab-out-multi-package.md) | `ab --out` over several packages keeps only the last package's artifact; a per-package path or a multi-section format is a contract change | user decision |
| [statistical-knob-derivation](statistical-knob-derivation.md) | `--count`/`--benchtime`/`--threshold` stay fixed statistics-grade defaults; deriving them from the measurement changes REQ-pew-sample-completeness and §10.1's comparability | user decision |
| [mcp-surface](mcp-surface.md) | pew serves the CLI only; whether an LLM reader is owed an MCP surface (or `run`/`ab`/`gc` a `--json`) is a product-scope call | user decision |
| [serve-proven-blocked-by-benchmark-loop](serve-proven-blocked-by-benchmark-loop.md) | every benchmark reaching `b.N`/`b.Loop` is unverifiable under the engine's benchmark-loop scan, so the serve-proven default serves nothing real yet | cross-tool train chunk 118 |
| [guidance-knobs-and-verb-table](guidance-knobs-and-verb-table.md) | §12's verb defaults table and the guidance knob blocks enumerate one contract twice; generating one from the other changes the fleet guidance format | user decision |
| [verdict-path-consolidation](verdict-path-consolidation.md) | the vouch set is assembled from four process-wide globals and admission decodes each row three times; collapse to one resolver value and one row decode | user decision |
| [gofresh-corpus-pin-lag](gofresh-corpus-pin-lag.md) | `go.mod` pins gofresh v0.95.0 while v0.97.0 is tagged; the two releases behind are chunk 138's evidence-root anchoring and bracket-root preflight, which gomutant already consumes (fleet sweep 2026-09-07) | cross-tool train chunk 118 (the seat of pew's next gofresh bump, shared with serve-proven-blocked-by-benchmark-loop; an earlier-ordered 98–101 ride is the same bump) |

## In-spec upgrade paths (tracked inline, not here)

Several design alternatives are documented at their spec sites as "upgrade paths on measured need."
They are kept current with the spec, so they need no separate tracking doc — listed here only as an
index:

- VTA call graph, if RTA ever over-includes (§7.4)
- escape-rule to skip loading stdlib bodies (§7.4)
- per-declaration hashing *into* cache deps (§7.7)
- gitignored persistent closure memo (§6)
- same-identity sample merge (§6)
