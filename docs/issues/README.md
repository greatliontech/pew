# Issue docs — deferred follow-ups

Tracked deferrals carrying a `Lands:` trigger. On resolution, the load-bearing rationale is promoted
inline to the spec / a test, and the doc is deleted (git holds history) — per the Issue triage
close-out convention.

| slug | summary | Lands |
|------|---------|-------|
| [remote-bench-execution](remote-bench-execution.md) | run measurements on a dedicated homelab bench machine: gRPC-over-SSH `pew agent`, machine lease, off-box builds, calibration drift-vet | capability charter (gofresh docs/plans/capability-charters.md) — activates when a dedicated bench machine is provisioned |
| [profile-capture-attribution](profile-capture-attribution.md) | subject attribution is consumer hand protocol: capture profiles with recordings, attribution verdict in status, profile diff in stat | cross-tool train chunk 15 |
| [per-arm-noise-floors](per-arm-noise-floors.md) | one global `--threshold` vs ±10% layout-only cross-commit drift on ns-class arms (run-to-run CV measured 0.2–1.4%); false regressions get hand-inspected — derive per-arm floors from lineage history | cross-tool train chunk 15 (keys on chunk 102's sliced closures) |
| [ab-worktree-placement-escape](ab-worktree-placement-escape.md) | ab's same-filesystem worktree refusal has no operator escape (`--worktree-dir` validated by the device check); crashed-run sibling residue wants a startup sweep | cross-tool train chunk 115 |
| [gitblob-linked-worktree-object-lookup](gitblob-linked-worktree-object-lookup.md) | pew run fails in linked worktrees ("gitblob: worktree status: object not found") — resolve the git common dir through the gitdir indirection | cross-tool train chunk 115 |
| [observed-fingerprint-recording-path](observed-fingerprint-recording-path.md) | plain-Capture recordings leave every true-external-effect benchmark permanently unverifiable; adopt CaptureObserved per arm and retire §7.8's no-proof sentence | cross-tool train chunk 127 (opens with a design discussion) |
| [verdict-ladder-shared-admissibility](verdict-ladder-shared-admissibility.md) | status and stat hand-order the same recording-admissibility ladder (format → dynamic-state strategy → verdict); collapse to one shared function so a new stale class cannot land on one surface only | cross-tool train chunk 115 |

## In-spec upgrade paths (tracked inline, not here)

Several design alternatives are documented at their spec sites as "upgrade paths on measured need."
They are kept current with the spec, so they need no separate tracking doc — listed here only as an
index:

- VTA call graph, if RTA ever over-includes (§7.4)
- escape-rule to skip loading stdlib bodies (§7.4)
- per-declaration hashing *into* cache deps (§7.7)
- gitignored persistent closure memo (§6)
- same-identity sample merge (§6)
| [repo-level-vouch-source](repo-level-vouch-source.md) | vouch sets live only as flags, hand-mirrored (tugboat CLAUDE.md + the fleet sweep); the unsafe drift direction is silent | rides cross-tool train chunk 115 |
