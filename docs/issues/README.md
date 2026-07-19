# Issue docs — deferred follow-ups

Tracked deferrals carrying a `Lands:` trigger. On resolution, the load-bearing rationale is promoted
inline to the spec / a test, and the doc is deleted (git holds history) — per the Issue triage
close-out convention.

| slug | summary | Lands |
|------|---------|-------|
| [buildconfig-completeness](buildconfig-completeness.md) | PGO profile content and CLI build-flag pass-throughs are absent from the buildconfig guard | when PGO benchmarks see real use, or `pew run` grows build-flag flags |
| [json-output](json-output.md) | `-json` machine-readable output for `status`/`stat` | when pew is first wired into CI/scripting beyond exit-code gating |
| [purity-directives](purity-directives.md) | impure benchmarks have no durable in-source form and must be re-specified per invocation | when `--impure` re-specification proves friction |
| [remote-bench-execution](remote-bench-execution.md) | run measurements on a dedicated homelab bench machine: gRPC-over-SSH `pew agent`, machine lease, off-box builds, calibration drift-vet | when a dedicated bench machine is provisioned and measurements first need to run on it |
| [quiesce-signal-fidelity](quiesce-signal-fidelity.md) | governor read from cpu0 only, boot-cumulative throttle counters, conflicting turbo signals resolve toward enabled | when a recorded run-conditions value or quiesce warning is observed wrong on a real machine |
| [stat-explain](stat-explain.md) | implement the spec-reserved `pew stat --explain` guard/input view | when guard-failure opacity bites in real use |
| [stream-config-key-poisoning](stream-config-key-poisoning.md) | a `key: value`-shaped log line interleaved into the `go test` stream is recorded as configuration and can fragment `stat` grouping | when a stream-derived configuration key is observed in a real recording, or before pew targets a package logging `key: value`-shaped lines to stdout |
| [stale-shape-recording-visibility](stale-shape-recording-visibility.md) | recordings failing the current shape are silently invisible to `stat` (all modes when both sides predate it) and to `gc` (never removed, never reported) | when the recording shape next changes, or a silently invisible recording confuses a real invocation |

## In-spec upgrade paths (tracked inline, not here)

Several design alternatives are documented at their spec sites as "upgrade paths on measured need."
They are kept current with the spec, so they need no separate tracking doc — listed here only as an
index:

- VTA call graph, if RTA ever over-includes (§7.4)
- escape-rule to skip loading stdlib bodies (§7.4)
- per-declaration hashing *into* cache deps (§7.7)
- gitignored persistent closure memo (§6)
- same-identity sample merge (§6)
