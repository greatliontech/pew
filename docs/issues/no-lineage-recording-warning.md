# Recording into an empty variant lineage is silent

## Problem

Go benchmark result names embed GOMAXPROCS (`BenchmarkX/cell-24`),
so a `--pin` run on a 24-thread host mints `-4`-suffixed names the
store has never seen. `pew run` records them without comment; the
mistake surfaces only later, at `pew stat`, as rows with nothing to
compare against — after the measurement time is already spent and
possibly after the operator believed a curve was on record. Field
occurrence: tugboat chunk 6, where a protocol document said `--pin`
while the store's lineage was unpinned; the incomparable recording
was discovered a full recording cycle later.

## Direction

At record time, when a produced benchmark line-name has no prior
recording in the store while a sibling name differing only in the
GOMAXPROCS suffix does, say so: "new variant lineage (-4); nearest
stored lineage is -24 — comparisons will not bridge". Warning, not
refusal: first recordings of genuinely new arms and deliberate
profile changes are legitimate; the operator just must not learn
about lineage divergence from a later stat.

## Lands

Cross-tool train chunk 44 (the recording-trust batch touches `run`'s
store interaction).
