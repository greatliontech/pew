# gofresh corpus pin lags the latest release (fleet sweep 2026-10-19)

The 2026-10-19 weekly fleet sweep's shape-corpus check reported this module
pinned to gofresh v0.117.6 while the latest release tag is v0.118.0 (the
remote is the authority; the local clone may lag it). Stipulator pins
v0.118.0 and reads current in the same sweep; gomutant pins v0.115.1 and
carries its own filing. Version lag is the one drift channel the shape
corpus leaves open — corpus content drift is unrepresentable — so a
consumer behind the release judges purity, inputs and freshness with an
older corpus than the one released. The reported latest tag is historical,
not a claim about the remote today.

The 2026-10-12 sweep's pin-lag filing (v0.115.1 against v0.116.1) closed
when the pin moved to v0.117.6 at the active plan's 4.3; this is a new
release past that pin, not that fact persisting. A bump is not a version
edit alone: it carries whatever producer and checker migration obligations
the release introduces, followed by a clean install so the installed binary
and the pinned corpus agree. Record the adopted release and the
compatibility decisions it forced. No dependency update, build or sweep was
run for this filing.

Lands: tooling-redesign 5.1w.7 — the shared library's release followed by
consumer adoption and install, which is pew's next scheduled gofresh bump.
An earlier bump closes it sooner; the closing observation is the next
sweep's shape-corpus line reading `current`.
