# Per-arm noise floors from lineage history

## Problem

`pew stat` applies one global regression floor (`--threshold`,
default 3%) to every benchmark. Nanosecond-scale arms measuring
small hot functions drift ±10% mixed-sign across commits whenever
ANY surface of the measured package changes — binary layout
(alignment, inlining neighborhoods), not the measured code. Field
evidence (tugboat node arms, 2026-08): run-to-run CV is 0.2–1.4%
(measured pinned and unpinned alike, 2 ns–30 ms subjects), so the
cross-commit ±10% is decisively not draw noise — it is a per-arm
property of subject size vs layout sensitivity.

Consequence: every layout-perturbing commit trips false regression
verdicts on those arms, and the consumer dispositions them by hand
inspection of the diff ("no touched path reaches this subject").
That reinstates exactly the eyeball judgment the significance
verdict exists to remove, and trains consumers to shrug at flags.

## Direction

Derive each arm's empirical noise floor from its own lineage
history: the cross-commit distribution of recordings whose measured
closure did not change (the stored provenance — closure and
runtime-input digests — already identifies same-content re-records
and layout-only neighbors). The verdict bar becomes
max(global threshold, arm floor), and `stat` names the bar it
applied per row so a verdict is auditable. Arms with insufficient
history fall back to the global threshold unchanged. A
hand-annotated per-arm floor may serve as the interim or override
surface, but the derived floor is the design goal — annotations rot.

## Lands

Cross-tool train chunk 15: folds into its opening design discussion
(both grow what a recording
carries about its own trustworthiness).
