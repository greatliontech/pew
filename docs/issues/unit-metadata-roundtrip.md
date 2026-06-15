# Round-trip Unit-metadata records

Lands: 5 — when `pew run` parses real `go test -bench` output, determine whether the toolchain
emits `Unit` metadata lines and, if so, preserve them through store round-trip.

## Context

The Go benchmark format has three record kinds (`benchfmt`): `Result`, `SyntaxError`, and
`UnitMetadata` — `Unit <unit> <key>=<value>` lines that describe how to interpret a unit (e.g.
`better=lower`, `assume=nondeterministic`), which `benchstat` uses. `internal/store` round-trips
`Result` records (with their config) and, as of chunk 1, **rejects** a `UnitMetadata` record on read
rather than silently dropping it (`store.go` Read `default` case). pew never writes such lines, so
its own recordings never contain them — the reject path is currently unreachable for pew-produced
files, but it guarantees no silent loss.

## The deferred question

At chunk 5, when `go test -bench` output is parsed and demuxed, check whether the toolchain (or a
benchmark via `b.ReportMetric` + a registered unit) emits `Unit` lines. If it does, the store must
**preserve** them through write+read — not reject — or recordings lose unit semantics benchstat
relies on (spec §10). That likely means round-tripping the full record stream, not just `[]*Result`.

## Resolution

On landing:
- If go test emits `Unit` lines → extend store Write/Read to carry `UnitMetadata` with an
  order-preserving round-trip test; promote this rationale into the store doc; delete this doc.
- If it never emits them → the chunk-1 reject-on-read is the permanent correct behavior; record that
  in the store doc; delete this doc.

git holds history either way.
