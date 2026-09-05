# `ab --out` holds only the last package of a multi-package comparison

`pew ab --out FILE ./...` writes one artifact per package to the same
path, each rewrite replacing the previous package's: after the run the
file holds only the last package compared (and, since the artifact is
now rewritten per completed iteration pair, the file changes hands
`count` times per package). The artifact's own shape is single-package
(`pkg:` header, two sides); a multi-package artifact needs either a
per-package path (`FILE.<package>`) or a multi-section format the
consumers read — a change of the `--out` contract.

Invariants preserved: the artifact stays marked `pew-ab`/`dirty`,
never a stat baseline; each package's artifact holds exactly its own
two streams.

Lands: user decision
