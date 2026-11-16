# Separating Pre-`ResetTimer` Fixture Setup from Hot-Loop Samples in Diagnostic Profiles

- **Status:** Open
- **Governing contract:** [`docs/specs/spec.md`](../specs/spec.md) (`REQ-pew-profile-capture`, `REQ-pew-profile-attribution`), [`docs/guidance.md`](../guidance.md)
- **Lands:** awaits triage

## Problem

Go's `-test.cpuprofile` and `-test.memprofile` instrument the entire test-binary process lifecycle — from runtime/package `init` before testlog through `b.run1` and every `b.launch`/`b.runN` calibration step to post-benchmark flush — rather than resetting when a benchmark calls `b.ResetTimer()` (`REQ-pew-profile-capture`: *"The capture scope is the process profiling window, including harness/setup work"*). Under duration budgets (`--profile-benchtime=1s`), classic `for i := 0; i < b.N; i++` benchmarks also re-execute pre-`b.ResetTimer()` setup on each `b.N` calibration step (`1`, `100`, `10000`, …).

On benchmarks that construct an in-memory or on-disk fixture before `b.ResetTimer()` and then run a fast or zero-allocation timed loop (for example a `0 B/op, 0 allocs/op` point-read benchmark over a 100k-entry seeded structure), 99–100% of `alloc_objects` and `alloc_space` samples — and a large share of `cpu` samples — belong to pre-`ResetTimer()` fixture construction (`fmt.Sprintf`, `bytes.Clone`, seed builders). In `pew`'s flat/cumulative symbol table, a `0 allocs/op` benchmark therefore appears to allocate hundreds of megabytes unless the reader manually cross-checks call stacks against the `Benchmark*` source lines before and after `b.ResetTimer()`.

## Resolution Scope

1. Investigate static/call-stack phase tagging in `internal/profiles` using the captured `Benchmark*` source AST and call-site line numbers stored in `sources`:
   - Identify unambiguous `b.ResetTimer()` / `b.Loop()` boundaries inside the top-level `Benchmark*` function (or `b.Run` sub-benchmark closure) in the captured source snapshot.
   - Tag or partition samples rooted at call sites preceding the timer reset/loop entry (`setup/init`) versus call sites inside the timed loop (`loop`), while keeping unclassified/ambiguous/background stacks explicitly labelled and conserving 100% of raw profile sample weights per `REQ-pew-profile-attribution`.
2. Surface this phase breakdown in profile reports (and highlight when recorded measurement rows report `0 allocs/op` while process-window allocations originate outside the timed loop) without overclaiming per-operation causal attribution.
