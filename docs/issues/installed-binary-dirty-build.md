# Installed binary built from a dirty tree (fleet sweep 2026-10-05)

The weekly fleet sweep's binary-provenance check reports the installed
`pew` (`~/go/bin/pew`) stamped `vcs.revision=36df236f8ce4` with
`vcs.modified=true`: DIRTY-BUILD. The revision is a docs-only
descendant of the last build-input commit ad6dda8ae41d (the format
rung judged once), so this is not a SKEW — the binary carries the
latest landed inputs — but the stamp says the tree it was compiled
from carried uncommitted changes, and the toolchain records only that
there were some. 36df236f8ce4 is the 2026-09-30 commit of
loader-refuses-pre-format-3-ledger-lines-remedy-circular, and
gofresh-pin-behind-the-toolchain-audit records that tugboat's session
rebuilt the installed binaries from HEAD that day with its field
report still uncommitted in this tree — so the likely dirtiness is a
docs file and the compiled inputs are ad6dda8ae41d's. The stamp cannot
say so, the sweep reads the stamp, and a binary the check cannot
anchor to a commit is one it cannot vouch for.

The standing response is the SKEW response: `go install` in this repo
from a clean tree (committed HEAD, `git status` empty). The sweep's
next provenance line reading `match` without the flag is the check
that closes this doc.

Scanned: the index and all 16 docs it lists. gofresh-corpus-pin-lag
tracks the pin, not the binary (rows disjoint; its trigger is the one
adopted below). gofresh-pin-behind-the-toolchain-audit tracks the
pin's audit listings — the binary/ambient toolchain skew it mentions
was fixed by the very rebuild that set this stamp; rows disjoint.
loader-refuses-pre-format-3-ledger-lines-remedy-circular and
strategy-stale-arms-flipped-valid-without-rerecord are store-reading
defects. No doc covers binary provenance.

Lands: cross-tool train chunk 291 (the gofresh bump; the pin-lag doc's
trigger, adopted — the bump's close-out rebuilds and reinstalls from
the committed tree, which is this doc's whole remedy); an earlier pew
session's clean install closes it sooner.
