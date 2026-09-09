# gofresh pin lags the latest release: v0.95.0 pinned, v0.97.0 tagged (fleet sweep 2026-09-07)

The weekly fleet sweep's shape-corpus check found this repo's `go.mod`
pinning `github.com/greatliontech/gofresh v0.95.0` while the remote
tags v0.97.0. The two releases behind are both the engine side of
train chunk 138: v0.96.0 anchors runtime-input revalidation at an
evidence root above the module and lets an absolute directory bracket
root walk its tree; v0.97.0 adds `Bracket.Reason`, a producer's
preflight of its declared roots. gomutant consumed both; pew and
stipulator did not.

Corpus content drift is unrepresentable — the corpus rides the module
version — so version lag is the one drift channel the sweep can see.
Until the bump, pew's recordings and verdicts are judged under the
v0.95.0 engine: a benchmark package declaring an absolute directory
bracket root, or a relative in-tree one, gets the pre-138 treatment
here while gomutant's judgment of the same declaration differs. The
bump is the whole remedy; the train's release-then-bump doctrine has
the consumer bump ride the consumer's next change set.

Lands: cross-tool train chunk 229
pew's next gofresh bump (serve-proven-blocked-by-benchmark-loop.md
already waits on that bump; one bump carries both, this doc mints no
second trigger). Should the Band D field-mass triage order a 98–101
consumer-bump ride ahead of 118, that ride is the bump and closes this
doc there.
