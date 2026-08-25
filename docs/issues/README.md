# Issue docs — deferred follow-ups

Tracked deferrals carrying a `Lands:` trigger. On resolution, the load-bearing rationale is promoted
inline to the spec / a test, and the doc is deleted (git holds history) — per the Issue triage
close-out convention.

| slug | summary | Lands |
|------|---------|-------|
| [gofresh-six-version-integration](gofresh-six-version-integration.md) | v0.70-installed vs v0.82-current: six versions + the DynamicStateStrategy recording field, then re-judge the wal reading | with the tool-phase pew visit, after the gofresh go1.27 fix |
| [remote-bench-execution](remote-bench-execution.md) | run measurements on a dedicated homelab bench machine: gRPC-over-SSH `pew agent`, machine lease, off-box builds, calibration drift-vet | when a dedicated bench machine is provisioned and measurements first need to run on it |
| [profile-capture-attribution](profile-capture-attribution.md) | subject attribution is consumer hand protocol: capture profiles with recordings, attribution verdict in status, profile diff in stat | cross-tool train chunk 15 |
| [per-arm-noise-floors](per-arm-noise-floors.md) | one global `--threshold` vs ±10% layout-only cross-commit drift on ns-class arms (run-to-run CV measured 0.2–1.4%); false regressions get hand-inspected — derive per-arm floors from lineage history | cross-tool train chunk 15 |
| [ab-worktree-placement-escape](ab-worktree-placement-escape.md) | ab's same-filesystem worktree refusal has no operator escape (`--worktree-dir` validated by the device check); crashed-run sibling residue wants a startup sweep | when a consumer first hits the unwritable-parent or mount-boundary refusal, or the next ab-surface train chunk |
| [gitblob-linked-worktree-object-lookup](gitblob-linked-worktree-object-lookup.md) | pew run fails in linked worktrees ("gitblob: worktree status: object not found") — resolve the git common dir through the gitdir indirection | user decision |
| [observed-fingerprint-recording-path](observed-fingerprint-recording-path.md) | plain-Capture recordings leave every true-external-effect benchmark permanently unverifiable; adopt CaptureObserved per arm and retire §7.8's no-proof sentence | its own train chunk (spec-level verdict-model change) |

## In-spec upgrade paths (tracked inline, not here)

Several design alternatives are documented at their spec sites as "upgrade paths on measured need."
They are kept current with the spec, so they need no separate tracking doc — listed here only as an
index:

- VTA call graph, if RTA ever over-includes (§7.4)
- escape-rule to skip loading stdlib bodies (§7.4)
- per-declaration hashing *into* cache deps (§7.7)
- gitignored persistent closure memo (§6)
- same-identity sample merge (§6)
