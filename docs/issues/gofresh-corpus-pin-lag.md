# gofresh corpus pin lags the latest release (fleet sweep 2026-09-28)

The weekly fleet sweep's shape-corpus check reports this repo's gofresh
pin at v0.105.1 against a latest remote tag of v0.107.0: LAG. The pin
is chunk 275's bump (internal/gotool's composition, the sampler, the
vouch grammar, the guidance projections, and the fingerprint field
reads deleted for gofresh's published forms; behind gofresh 265, 266,
274, and 279). gofresh has released twice since: the local gofresh
clone, last fetched 2026-09-22, holds tags through v0.105.1 and its
`origin/main` sits at the 279 landing, so what v0.106.0 and v0.107.0
carry is not readable from this machine — the remote is the authority
the sweep reads, and CI mints tags the local clone lags. Corpus
content drift is unrepresentable; version lag is the one drift channel
left, and 279 declared that every consumer's machine-local records
re-measure once at the bump that follows it (pew's store re-measures
once at each bump, as 275 did).

Scanned: the index and all 12 docs. environment-normalized-once is
slotted "chunk 252, after 275" (the landed bump) and
format-rung-two-readers closed at 275; none schedules a gofresh bump
on its own, so this doc mints no second trigger for scheduled work —
no train chunk charters the bump either: gofresh 281's charter names
"the consumers' next bumps" (pew's runCommand containment copy deletes
there) without numbering them, and pew's queued chunks (174, 252, 173,
232, 276, 177) carry no bump rider.

Re-observed by the 2026-10-05 sweep: the latest remote tag is now
v0.108.3 (gomutant and stipulator are pinned there and read current);
this repo's pin is unchanged at v0.105.1, so the lag has grown from two
releases to the v0.106.0–v0.108.3 series. gofresh-pin-behind-the-
toolchain-audit (filed 2026-09-30 from tugboat, awaiting triage) names
the same bump as the fix for the host's go1.27.1-dst.13 listing, so the
bump now carries two docs' facts; the trigger is unchanged.

Lands: cross-tool train chunk 291 (the pew bump behind gofresh 279 and
281; slotted at the 2026-09-29 replan).