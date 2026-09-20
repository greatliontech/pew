# environment-normalized-once

Environment validity — a duplicated, malformed, or NUL-bearing entry,
gofresh's `NormalizeEnv` — is decided by a verb's inputs alone, yet the
tree judges it at three sites: `internal/gotool.run` (the runner path:
the toolchain sample and the `go env` probes, all at preparation),
`internal/run.runCommand` (every measurement spawn, per package) and
`internal/run.IngestObservation` (after the measurement). The two
downstream branches are unreachable: every verb's package listing
(`resolvePackages` → `gotool.Run` over the process environment) is the
first normalizer, before any build or measurement — `ab`, which takes
no provenance sample, included — and the sample is a second instance
for `run`, `status`, and `stat`; the measured environment is that same
value with `GOMAXPROCS` replaced (`pinEnvironment` drops every
`GOMAXPROCS=` entry before appending one, or returns the environment
untouched), so it cannot introduce a key. A probe ignoring either
downstream error survives the whole `internal/run` package — a dead
guard, not a test gap.

Collapse: normalize once, at preparation, into a typed normalized
environment (`gotool.Environment`, one refusing constructor over the
inherited environment) threaded through the env-taking signatures
(`newEnvironments`/`pinEnvironment`, `ExecuteContext`/`BuildContext`/
`runCommand`, `ExecuteBinaryContext`, `IngestObservation`,
`EffectiveGoflags`, `ReadTargetPlatform`, `PGOInput`, the engine
constructors, the sampler and its memo key, `ab`'s side builds and
guards); downstream, `For(resolvedDir)` pins `PWD` over an
already-normalized value, so the refused class is unrepresentable past
preparation and the two dead branches (and the comment naming this
doc) go with it. Invariant preserved: REQ-pew-preparation's
input-decidable refusals before the first measurement — true today by
reachability, structural after.

Lands: cross-tool train chunk 252 (the hygiene sweep), built over gofresh 265's environment
setter once 275 has consumed it — so the typed environment is gofresh's, not a fourth spelling
(re-slotted at audit 264).
