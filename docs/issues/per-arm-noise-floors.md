# Historical per-arm variation and explicit tolerances

Lands: Gofresh docs/plans/tooling-redesign.md 8.2.

## Historical report

`pew stat` exposes one regression tolerance (`--threshold`, default 3%).
The August 2026 tugboat node-arm report described mixed-sign cross-commit
movement of approximately ±10% on nanosecond-scale arms, beside run-to-run
CV of 0.2–1.4% over pinned and unpinned subjects spanning 2 ns–30 ms.
The report attributed the larger movement to layout sensitivity (alignment
and inlining neighborhoods) and described manual diff inspection to dismiss
regression flags. These are retained historical observations and their
reported interpretation, not a new measurement or proof that a flagged
change is harmless. Unchanged closure evidence alone does not establish
layout as the cause or exclude a meaningful performance change.

## Remaining obligation

Active item 8.2 rejects the earlier proposal to use
`max(global threshold, arm floor)` as the verdict bar. Empirical historical
spread is not proof of harmless noise and must not automatically raise the
regression threshold. Retain explicit tolerances and report admitted variation
with its sample population, lineage, conditions and missing support visible.
An insufficient history is an insufficient history, not a derived safe floor.

Item 8.1 owns admission of historical support without adding present-day
observations or provenance-authenticity claims. Item 7.6 owns enforcing
policy: omitted options still resolve in code to `partial/report/report`,
whereas `REQ-pew-comparison-policies` requires `complete/require/compatible`
for an enforcing comparison. That declared spec gap is not solved by a
variation report. The independent `--confidence` control also remains;
7.6 derives displayed confidence from significance unless a real independent
workflow justifies retaining it. Neither change licenses automatic tolerance
inflation.
