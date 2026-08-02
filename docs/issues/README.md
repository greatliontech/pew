# Issue docs — deferred follow-ups

Tracked deferrals carrying a `Lands:` trigger. On resolution, the load-bearing rationale is promoted
inline to the spec / a test, and the doc is deleted (git holds history) — per the Issue triage
close-out convention.

| slug | summary | Lands |
|------|---------|-------|
| [bench-scratch-dirs-recorded-as-runtime-inputs](bench-scratch-dirs-recorded-as-runtime-inputs.md) | run-created package-dir scratch paths land in the runtime-input manifest: noise at any size, permanent verification churn | with the next observation-strategy revision (gofresh runtimeinput classification), or sooner on a second consumer's churn |
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
- **[one-go-invocation-environment](one-go-invocation-environment.md)** — gotool.RunIn inherits
  `$PWD` while run.Execute pins it, so go list can report a symlink-alias module dir the
  measurement machinery bridges case-by-case; one pinned-environment mechanism would hold by
  construction. *Lands: the next pew work item.*
