# Installed binary trails the repo's last build input (fleet sweep 2026-10-19)

The 2026-10-19 weekly fleet sweep's binary-provenance check reported the
installed `pew` stamped `vcs.revision=7683672d6e03` while the repo's last
build-input commit is `8af53d13881a`: SKEW. The binary anchors cleanly to a
commit (no modified flag), so its content is recoverable from history, but
that commit is the one that fixed diagnostic executable retention, and the
tree has since taken at least one further build-input commit. Every `pew`
invocation on this machine — the sweep's own store-freshness check among
them — therefore runs code older than HEAD's, and a verdict it renders
cannot be attributed to the current tree. The skew guards refuse loudly at
use; the sweep catches the drift before use.

The remedy is the standing one: `go install` from the committed tree, which
moves the provenance line back to `match`. Installation covers the binary
file; a long-lived reader process keeps the old code until it restarts.
This filing records the fact and performs no install.

The 2026-10-12 sweep's skew filing (e73e3153787e against d5aec0a77322)
closed at the active plan's 4.6 install; this is the same drift class
reopened by later commits, not that fact persisting.

Lands: tooling-redesign 5.1w.7 — the release step installs only consumers
whose own acceptance gates have passed, which is pew's next scheduled clean
install. An earlier `go install` from pew's committed HEAD closes it sooner;
the closing observation is the next sweep's provenance line reading `match`.
