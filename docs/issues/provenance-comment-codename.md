# Planning codename in a kept-current comment (`provenance.go`)

Lands: when `internal/provenance` is next touched

## Fault

`internal/provenance/provenance.go`, doc comment on `buildConfig`:

> Run-flag-derived parts (-tags, -gcflags, PGO) integrate in chunk 5.

"chunk 5" is a plan-local codename; per the artifact-homes contract, planning codenames never
appear in kept-current artifacts. The comment is also stale as a status claim (the run command
exists; the buildconfig gap it alludes to is tracked separately).

## Resolution

One-line comment edit: drop the chunk reference and point the remaining-gap statement at
`docs/issues/buildconfig-completeness.md` rationale-wise — but per the no-cite invariant a
kept-current comment must not cite a tracking artifact, so the comment should simply state the
current fact ("run-flag-derived inputs (-tags, -gcflags, PGO) are not yet part of the digest") and
leave tracking to the buildconfig-completeness issue's own index entry.
