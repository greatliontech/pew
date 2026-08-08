# Issue docs — deferred follow-ups

Tracked deferrals carrying a `Lands:` trigger. On resolution, the load-bearing rationale is promoted
inline to the spec / a test, and the doc is deleted (git holds history) — per the Issue triage
close-out convention.

| slug | summary | Lands |
|------|---------|-------|
| [recorded-config-trust](recorded-config-trust.md) | whitelisted toolchain-key values are spoofable in-stream; historical foreign keys unpoliced at read time | cross-tool train chunk 44 |
| [remote-bench-execution](remote-bench-execution.md) | run measurements on a dedicated homelab bench machine: gRPC-over-SSH `pew agent`, machine lease, off-box builds, calibration drift-vet | when a dedicated bench machine is provisioned and measurements first need to run on it |
| [per-benchmark-view-builds](per-benchmark-view-builds.md) | verdict surfaces build one analysis view per benchmark (two per inert-growth rider); a per-package batch check would share it | cross-tool train chunk 44 |
| [profile-capture-attribution](profile-capture-attribution.md) | subject attribution is consumer hand protocol: capture profiles with recordings, attribution verdict in status, profile diff in stat | the cross-tool train's profile chunk (gofresh docs/plans/cross-tool-train.md) |
| [derivation-ab-mode](derivation-ab-mode.md) | uncommitted-vs-ref A/B is a hand stash cycle in consumers; `pew ab` — both sides built first (worktree for the ref), interleaved runs, no tree mutation | cross-tool train chunk 44 |

## In-spec upgrade paths (tracked inline, not here)

Several design alternatives are documented at their spec sites as "upgrade paths on measured need."
They are kept current with the spec, so they need no separate tracking doc — listed here only as an
index:

- VTA call graph, if RTA ever over-includes (§7.4)
- escape-rule to skip loading stdlib bodies (§7.4)
- per-declaration hashing *into* cache deps (§7.7)
- gitignored persistent closure memo (§6)
- same-identity sample merge (§6)
