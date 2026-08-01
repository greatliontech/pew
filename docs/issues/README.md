# Issue docs — deferred follow-ups

Tracked deferrals carrying a `Lands:` trigger. On resolution, the load-bearing rationale is promoted
inline to the spec / a test, and the doc is deleted (git holds history) — per the Issue triage
close-out convention.

| slug | summary | Lands |
|------|---------|-------|
| [recorded-config-trust](recorded-config-trust.md) | whitelisted toolchain-key values are spoofable in-stream; historical foreign keys unpoliced at read time | when a spoofed value or foreign-key fragmentation is observed, or when read-side recording validation is next designed |
| [remote-bench-execution](remote-bench-execution.md) | run measurements on a dedicated homelab bench machine: gRPC-over-SSH `pew agent`, machine lease, off-box builds, calibration drift-vet | when a dedicated bench machine is provisioned and measurements first need to run on it |
| [per-benchmark-view-builds](per-benchmark-view-builds.md) | verdict surfaces build one analysis view per benchmark (two per inert-growth rider); a per-package batch check would share it | when a package's verdict pass measurably dominates status/stat latency, or when the verdict surfaces are next restructured |

## In-spec upgrade paths (tracked inline, not here)

Several design alternatives are documented at their spec sites as "upgrade paths on measured need."
They are kept current with the spec, so they need no separate tracking doc — listed here only as an
index:

- VTA call graph, if RTA ever over-includes (§7.4)
- escape-rule to skip loading stdlib bodies (§7.4)
- per-declaration hashing *into* cache deps (§7.7)
- gitignored persistent closure memo (§6)
- same-identity sample merge (§6)
- **[bench-observation-evidence-path](bench-observation-evidence-path.md)** — recording verification is decorative for benchmarks: pew never wires the testlog+bracket observation harness its sibling tools use; the fix is the identical wiring. *Lands: first pew work item after gofresh's memo chunks land — before the gomutant train.*
