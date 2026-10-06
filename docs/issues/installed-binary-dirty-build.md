# Installed binary built from a dirty tree

The installed `pew` at `~/go/bin/pew` was reported with
`vcs.revision=36df236f8ce4` and `vcs.modified=true`. Its stamp cannot
identify the uncommitted inputs from which it was built, so its content
cannot be attributed to the named commit.

Reinstall from the clean committed tree and verify the installed build
information names that revision with `vcs.modified=false`.

Lands: performance-evidence plan chunk 2 (2.3).
