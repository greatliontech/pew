# gofresh corpus pin lags the latest release (fleet sweep 2026-10-12)

The weekly fleet sweep's shape-corpus check reports this module pinned
to gofresh v0.115.1 while the latest release tag is v0.116.1 (the
remote is the authority; the local clone may lag it). Version lag is
the one drift channel the shape corpus leaves open — corpus content
drift is unrepresentable — so a consumer behind the release judges
purity, inputs and freshness with an older corpus than the one
released. The remedy is the bump: `go get
github.com/greatliontech/gofresh@v0.116.1`, whatever adoption riders
the release carries, tests green, then the binary reinstalled (the
provenance doc's own condition). The sweep's next corpus line reading
`current` is the check that closes this doc.

Scanned: the index and all 9 docs it lists. None schedules a gofresh
bump: the 2026-09-28 pin-lag doc closed at the bump to v0.115.1 and
is gone; train-riders-for-the-performance-evidence-plan carries a
"residual dependency bump tracked by the train" — that is the train's
chunk 320 (pew's residual bump charter), which the active sequence
maps into the adoption chunk named below, so this doc adopts that
trigger rather than minting a second. Rows disjoint.

Lands: tooling-redesign 4.3 — pew's adoption of the released
capability for the first benchmark arm, the bump its opening step
(the train's rule is a producer's release before the consumer bump
that reads it; chunk 320 is the retained charter it maps to). An
earlier bump closes it sooner.
