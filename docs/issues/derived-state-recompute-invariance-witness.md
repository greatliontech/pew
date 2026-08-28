# derived-state-recompute-invariance-witness

`REQ-pew-derived-state` states two obligations: persisted closure
hashes are a memoization keyed *only* by `(commit, toolchain,
buildconfig)`, and recomputing or discarding them never changes a
validity verdict. The bound witnesses
(`TestStatRecordingPredatingDynamicStateKeyIsStale`,
`TestStatusRecordingPredatingDynamicStateKeyIsStale`) pin the
pew-side authority arm — persisted derived state whose derivation
strategy moved is never trusted across the strategy break — but
neither exercises the memo-key composition or a
recompute/discard-then-re-verdict path.

The unwitnessed clause is engine-owned: pew persists no closure-hash
cache of its own (its only memoization is the in-process go-version
sampler); the recompute path and its serving soundness live in the
gofresh engine, enforced by gofresh's own corpus (closure.md's
serve-iff-proven-unchanged discipline). A pew-side witness needs an
integration anchor over the gofresh seam: discard the engine's
cached derived state between two `pew status`/`pew stat` invocations
on an unchanged tree and assert the verdicts are byte-identical, and
vary an input outside `(commit, toolchain, buildconfig)` asserting
the memo key is unaffected.

A stipulator gap cannot carry this: the requirement already has
bound example coverage, so a `covered:` gap resolves instantly and a
per-clause shortfall is inexpressible in the gap model (the same
shape as gomutant's mcp-liveness cancellation clause).

**Lands:** when a pew-side integration anchor exercising the gofresh
engine's derived-state discard/recompute path lands in the test
surface (bind it to `REQ-pew-derived-state` and delete this doc).
