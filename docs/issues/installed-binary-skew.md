# Installed binary skewed from the tree (fleet sweep 2026-10-12)

The weekly fleet sweep's binary-provenance check reports the installed
`pew` (`~/go/bin/pew`) stamped `vcs.revision=e73e3153787e` while the
repo's last build-input commit is `d5aec0a77322`: SKEW. The binary
anchors to a commit (no modified flag), but the tree has taken
build-input commits since it was built, so the installed tool is not
the tree. The check is deliberately coarse — any commit moves HEAD,
docs included — and the standing response is equally cheap: `go
install` in this repo from a clean tree (committed HEAD, `git status`
empty), then close. The sweep's next provenance line reading `match`
is the check.

Scanned: the index and all 9 docs it lists. None touches the
installed binary's provenance; the 2026-10-05 dirty-build doc closed
at the v0.115.1 bump's install and is gone. Rows disjoint; no
scheduled trigger duplicated.

Lands: tooling-redesign 4.6 — the first benchmark arm's adoption
closes by installing only the clean committed consumer, which is this
doc's remedy; the sweep's next provenance line reading `match` closes
it, and an earlier `go install` from a clean tree closes it sooner.
