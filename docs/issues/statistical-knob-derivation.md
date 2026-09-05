# Deriving `--count`, `--benchtime`, and `--threshold` from the measurement

`run --count` (10), `run --benchtime` (1s), `ab --count` (6), and
`stat --threshold` (3%) are fixed statistical defaults. Each could be
derived from what the measurement itself shows: a sample count that
grows until the confidence interval narrows below a target width
(sequential sampling), a benchtime derived from the benchmark's own
per-op cost so every sample holds a comparable iteration count, and a
regression threshold derived from the baseline's own observed noise
floor (its coefficient of variation) rather than one magnitude for
every benchmark.

Each derivation changes a spec-level contract rather than a CLI
default:

- A derived sample count changes what REQ-pew-sample-completeness
  demands per recording — "exactly the demanded `--count` samples"
  becomes a per-benchmark stopping rule that the recording must state,
  and a stored recording's sample count would no longer be comparable
  across benchmarks by construction.
- A derived threshold flips `stat` verdicts (§10.1's fixed ≥3% floor is
  what makes two runs' verdicts comparable) and interacts with the
  per-arm noise floors already filed
  (`per-arm-noise-floors.md`).
- A derived benchtime changes the `-benchtime` provenance every
  recording carries in its runtime configuration.

Whether the statistics-grade defaults stay fixed (comparable by
construction) or become measurement-derived (adaptive, per benchmark)
is a judgment on comparability across a store's history.

Invariants preserved: every recording keeps stating the sample count
and benchtime it was measured with; a verdict remains reproducible from
the two recordings alone.

Lands: user decision
