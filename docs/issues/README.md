# Issue docs — deferred follow-ups

Tracked deferrals carrying a `Lands:` trigger. On resolution, the load-bearing rationale is promoted
inline to the spec / a test, and the doc is deleted (git holds history) — per the Issue triage
close-out convention.

| slug | summary | Lands |
|------|---------|-------|
| [batched-package-load](batched-package-load.md) | one shared `packages.Load` across packages instead of per-package whole-program loads | when closure performance is next worked, or multi-package status proves slow |
| [buildconfig-completeness](buildconfig-completeness.md) | PGO profile content + CLI build-flag pass-throughs in the buildconfig guard (cgo/env half landed) | when PGO benchmarks see real use, or `pew run` grows build-flag flags |
| [closure-list-cache](closure-list-cache.md) | memoize `go list -deps -test` per package (currently run per benchmark) | when closure performance is next worked, or `pew status` proves slow |
| [dirty-runtime-inputs](dirty-runtime-inputs.md) | dirty/pinned-baseline semantics for ignored or untracked runtime inputs | when changing dirty baseline checks or runtime-input baseline semantics |
| [json-output](json-output.md) | `-json` machine-readable output for `status`/`stat` | when pew is first wired into CI/scripting beyond exit-code gating |
| [provenance-capture-cache](provenance-capture-cache.md) | capture provenance once per module, not once per package | when status/run wall-clock proves slow, or the capture call sites change |
| [purity-directives](purity-directives.md) | durable `//pew:pure` / `//pew:external` code directives vs CLI flags | when CLI purity flags prove insufficient |
| [run-stale-bench-filter](run-stale-bench-filter.md) | `pew run --stale` discards the user's `--bench` pattern | when `pew run` benchmark selection or `--stale` handling is next changed |
| [stat-blob-reread](stat-blob-reread.md) | `pew stat` parses every historical recording blob twice | when stat inventory or side-reading code is next changed |
| [stat-explain](stat-explain.md) | implement the spec-reserved `pew stat --explain` guard/input view | when guard-failure opacity bites in real use |
| [status-label-flag](status-label-flag.md) | `--label` on `pew status` so labeled recordings get verdicts | when labeled-variant recordings see real use, or the status CLI is next extended |

## In-spec upgrade paths (tracked inline, not here)

Several design alternatives are documented at their spec sites as "upgrade paths on measured need."
They are kept current with the spec, so they need no separate tracking doc — listed here only as an
index:

- VTA call graph, if RTA ever over-includes (§7.4)
- escape-rule to skip loading stdlib bodies (§7.4)
- per-declaration hashing *into* cache deps (§7.7)
- `runconditions` provenance + comparison-mismatch warning (§9)
- gitignored persistent closure memo (§6)
- same-identity sample merge (§6)
