# Issue docs — deferred follow-ups

Tracked deferrals carrying a `Lands:` trigger. On resolution, the load-bearing rationale is promoted
inline to the spec / a test, and the doc is deleted (git holds history) — per the Issue triage
close-out convention.

| slug | summary | Lands |
|------|---------|-------|
| [purity-directives](purity-directives.md) | durable `//pew:pure` / `//pew:external` code directives vs CLI flags | when CLI purity flags prove insufficient |
| [unit-metadata-roundtrip](unit-metadata-roundtrip.md) | preserve `Unit` metadata records through store round-trip if go test emits them | 5 |

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
