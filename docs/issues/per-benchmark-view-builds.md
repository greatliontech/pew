# Verdict surfaces build one analysis view per benchmark; a batch check per package would share it

Lands: cross-tool train chunk 44.
## Observed

checkOne runs one engine.Check per benchmark (one SSA load each — the
pre-existing shape), and the inert-growth rule's re-check
(inertGrownRecheck) builds a second view per riding benchmark: a package
with N recorded benchmarks and an added sibling pays 2N view builds per
status invocation until a run refreshes the recordings. gofresh's
CheckBatch/CheckObservedBatch check a whole recording set over one view
with one shared window.

## Resolution shape

Group verdicts per package: one NewViewFor over the package's recorded
benchmarks, CheckBatch for the ordinary verdicts, and the inert-growth
rule's ledger read, capture, and re-check all against that same view.
The per-benchmark purity fold and the rule's per-recording gates are
unchanged; only the view construction is shared. The engine holds no
cross-view cache, so correctness is unaffected either way — this is
cost, not soundness.
