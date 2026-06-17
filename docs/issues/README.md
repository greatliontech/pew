# Issue docs — deferred follow-ups

Tracked deferrals carrying a `Lands:` trigger. On resolution, the load-bearing rationale is promoted
inline to the spec / a test, and the doc is deleted (git holds history) — per the Issue triage
close-out convention.

| slug | summary | Lands |
|------|---------|-------|
| [buildconfig-completeness](buildconfig-completeness.md) | exact buildconfig guard inputs, including cgo and PGO content | when changing buildconfig guard inputs or build-flag handling |
| [dirty-runtime-inputs](dirty-runtime-inputs.md) | dirty/pinned-baseline semantics for ignored or untracked runtime inputs | when changing dirty baseline checks or runtime-input baseline semantics |
| [purity-directives](purity-directives.md) | durable `//pew:pure` / `//pew:external` code directives vs CLI flags | when CLI purity flags prove insufficient |
| [quiesce-turbo-thermal](quiesce-turbo-thermal.md) | turbo and thermal checks missing from strict quiesce implementation | when changing quiesce checks, strict-mode run hygiene, or §9 quiesce documentation |
| [stat-historical-inventory](stat-historical-inventory.md) | `pew stat` should inventory recordings from selected refs, not only current source | when changing `pew stat` inventory or git-ref recording enumeration |

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
