# Serve-proven serves nothing that reads `b.N` or `b.Loop`

`run`'s default serves a benchmark whose recording is valid and
measures the rest (REQ-pew-serve-proven). Under the shared engine's
current benchmark-loop package scan, every benchmark that reaches
`testing.B.N` or `testing.B.Loop` — that is, every real benchmark —
verdicts `unverifiable (reaches testing.N (test runtime
configuration))` with every guard holding, so it is never served and
always re-measured; only an empty-bodied benchmark can serve. The
default is sound (it never serves what is unproven) but inert over a
real store until the engine audits the benchmark loop as harness
pacing.

Observed: a two-benchmark fixture with `for i := 0; i < b.N; i++ {}`
bodies, recorded then re-run over an unchanged tree, re-measures both;
`status --explain` shows every guard matching under the unverifiable
verdict. pew's serve-proven pins use empty-bodied benchmarks for this
reason.

Invariants preserved: no false valid; the closure hash, the
compartment hash, and every guard still stale a real change.

Lands: cross-tool train chunk 229
audit) — at pew's next gofresh bump after it, the serve-proven pins
gain a `b.Loop` body and this doc's rationale promotes into the
REQ-pew-serve-proven prose.
