# Issue docs — deferred follow-ups

Tracked deferrals carrying a `Lands:` trigger. On resolution, the load-bearing rationale is promoted
inline to the spec / a test, and the doc is deleted (git holds history) — per the Issue triage
close-out convention.

| slug | summary | Lands |
|------|---------|-------|
| [mutation-oracle-observation](mutation-oracle-observation.md) | changed-code measurement banks kills but cwd-sensitive integration observations are non-reusable and final evidence work reaches its deadline | performance-evidence plan chunk 4 |
| [guidance-purpose-column-second-enumeration](guidance-purpose-column-second-enumeration.md) | §12's opt-in purpose column restates the document's knob prose in its own words, pinned to nothing | cross-tool train chunk 232 |
| [remote-bench-execution](remote-bench-execution.md) | run measurements on a dedicated homelab bench machine: gRPC-over-SSH `pew agent`, machine lease, off-box builds, calibration drift-vet | capability charter (gofresh docs/plans/capability-charters.md) — activates when a dedicated bench machine is provisioned |
| [profile-capture-attribution](profile-capture-attribution.md) | subject attribution is consumer hand protocol: capture profiles with recordings, attribution verdict in status, profile diff in stat | cross-tool train chunk 15 |
| [per-arm-noise-floors](per-arm-noise-floors.md) | one global `--threshold` vs ±10% layout-only cross-commit drift on ns-class arms (run-to-run CV measured 0.2–1.4%); false regressions get hand-inspected — derive per-arm floors from lineage history | cross-tool train chunk 15 (keys on chunk 102's sliced closures) |
| [audit-note-renderers-one-grammar](audit-note-renderers-one-grammar.md) | compare renders an audit row's difference two ways (run conditions vs every other audit row): one renderer with a per-row comparison | cross-tool train chunk 252 |
| [environment-normalized-once](environment-normalized-once.md) | environment validity is judged at three sites where the inputs decide it once; the two downstream guards are dead — normalize once at preparation into a typed environment | cross-tool train chunk 252, after 275 |
| [observed-fingerprint-recording-path](observed-fingerprint-recording-path.md) | plain-Capture recordings leave every true-external-effect benchmark permanently unverifiable; adopt CaptureObserved per arm and retire §7.8's no-proof sentence | cross-tool train chunk 127 (autonomous since 2026-09-29; scope sharpened at audit 288) |
| [derived-state-recompute-invariance-witness](derived-state-recompute-invariance-witness.md) | `REQ-pew-derived-state`'s recompute/discard-invariance clause has no pew-side witness (engine-owned path; the bound tests pin only the strategy-break authority arm; a gap cannot express a per-clause shortfall) | cross-tool train chunk 232 |
| [spec-wide-requirement-forming](spec-wide-requirement-forming.md) | only §13 is REQ-formed; a test witnessing a §§1–12 contract has no id to bind against — convert sections on demand, same structure-only discipline | each performance-evidence chunk, its section; the home rule at the train's 232 residue |
| [ab-out-multi-package](ab-out-multi-package.md) | `ab --out` over several packages keeps only the last package's artifact; a per-package path or a multi-section format is a contract change | cross-tool train chunk 174 |
| [mcp-surface](mcp-surface.md) | pew serves the CLI only; whether an LLM reader is owed an MCP surface (or `run`/`ab`/`gc` a `--json`) is a product-scope call | cross-tool train chunk 177 for the JSON and progress step; the MCP surface when a driving agent appears (a checkable condition) |
| [verdict-path-consolidation](verdict-path-consolidation.md) | the vouch set is assembled from four process-wide globals and admission decodes each row three times; collapse to one resolver value and one row decode | cross-tool train chunk 174 |
| [gofresh-corpus-pin-lag](gofresh-corpus-pin-lag.md) | the gofresh pin (v0.105.1, the 275 bump) lags the latest release — v0.107.0 at filing (fleet sweep 2026-09-28), v0.108.3 by the 2026-10-05 sweep; the store re-measures once at the bump | performance-evidence plan chunk 2 |
| [strategy-stale-arms-flipped-valid-without-rerecord](strategy-stale-arms-flipped-valid-without-rerecord.md) | 46 tugboat arms read `stale (dynamic-state strategy)` under the v0.101.3-pinned pew and `valid` under the v0.102.0 one with the store untouched — one verdict is wrong and the ladder does not say which (tugboat field report 2026-09-29) | a rider on cross-tool train chunk 174 (audit 288: the flip is the store's — a valid arm carries pew-format: 3 — not a wrong verdict; the work is the explain row per validity key and the operands on the stale line) |
| [loader-refuses-pre-format-3-ledger-lines-remedy-circular](loader-refuses-pre-format-3-ledger-lines-remedy-circular.md) | the store loader refuses pre-format-3 recordings whose ledger line exceeds the scanner bound — five of tugboat's nine package stores — and the remedy it names cannot run over a refused store (tugboat field report 2026-09-30) | performance-evidence plan chunk 3 |
| [gofresh-pin-behind-the-toolchain-audit](gofresh-pin-behind-the-toolchain-audit.md) | the gofresh pin (v0.105.1) predates the toolchain audit listings the host runs under, so every judged run degrades to the unaudited posture (tugboat field report 2026-09-30) | performance-evidence plan chunk 2 |
| [installed-binary-dirty-build](installed-binary-dirty-build.md) | the installed binary is stamped at a docs-only descendant (36df236f8ce4) of the last build-input commit with `vcs.modified=true` — compiled from a tree with uncommitted changes, so its content corresponds to no commit (fleet sweep 2026-10-05); the standing `go install` from a clean tree clears it | performance-evidence plan chunk 2 (2.3) |

## In-spec upgrade paths (tracked inline, not here)

Several design alternatives are documented at their spec sites as "upgrade paths on measured need."
They are kept current with the spec, so they need no separate tracking doc — listed here only as an
index:

- VTA call graph, if RTA ever over-includes (§7.4)
- escape-rule to skip loading stdlib bodies (§7.4)
- per-declaration hashing *into* cache deps (§7.7)
- gitignored persistent closure memo (§6)
- same-identity sample merge (§6)
