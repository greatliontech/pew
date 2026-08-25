# Collapse the per-surface recording-admissibility ladders

`status` and `stat` each hand-order the same admissibility ladder over
a stored recording before any guard verdict: exact format validation
(`stale (format)`), dynamic-state strategy currency (`stale
(dynamic-state strategy)`), then the fingerprint read and the engine
verdict. `status` runs it inside `checkPackage`/`fingerprintFromConfig`
(cmd/pew/status.go), `stat` as a per-side skip chain
(`recordingCurrent` → `recordingStrategyCurrent`, cmd/pew/stat.go) —
two orderings of one contract (spec §7's prerequisites), kept aligned
only by parallel tests.

Collapse: one shared admissibility function (recording → admissible |
stale class + reason) consumed by both surfaces, so a new stale class
lands in one place and cannot land on one surface only. `run --stale`
reaches the ladder through status's path and needs no third copy.

Invariants preserved: format failure judged without interpreting any
guard or purity field (§7); a strategy mismatch never compared as a
baseline; per-side (not per-pair) attribution of stat's skip warnings.

The shared function needs a per-side "is this side the working
tree?" input: the strategy rung applies only to the working-tree
side (re-recordable, verdict-bearing — stat's newSideIsWorkingTree,
status, run --stale) — every ref-resolved side (pinned tag, auto's
HEAD, either A/B ref) enters no verdict, cannot be re-run into, and
compares with a strategy difference surfacing as compare's audit
note (spec §5's strategy row, §7's exclusion) — while the format
rung applies everywhere (an unreadable recording serves no surface).

Also fold in: the ladder's gates inspect only the first result row
(`recordingCurrent`/`recordingStrategyCurrent` read `recs[0]`), so a
recording whose rows disagree past row 0 passes both gates and
surfaces only later as compare's mixed-provenance note — the shared
admissibility function should judge the whole recording, making the
row-0 blind spot unrepresentable.

Lands: the next chunk that extends the admissibility ladder — a new
stale class or a new verdict surface.
