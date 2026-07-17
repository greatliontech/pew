# Recordings failing the current shape become silently invisible to `stat` and `gc`

Lands: when the recording shape next gains or changes a mandatory field, or when a silently
invisible recording confuses a real `pew stat`/`pew gc` invocation

## Gap

A stored recording whose parsed config fails `store.IsRecordingShape` (it predates a
mandatory provenance field — `runtimeconfig`, `pew-runtime*`, `pew-runconditions` each created such
a cohort) is skipped without any diagnostic on two paths:

- **`pew stat` inventory** (`cmd/pew/stat.go`): `addRefInventory` and the working-tree branch of
  `addStatInventory` drop shape-failing recordings before they enter `m.keys`. The per-benchmark
  "stale (format); skipping — re-run `pew run`" warning in `runStat` fires only for inventoried
  keys, so it covers a stale-shape side only when the *other* side still passes the shape check.
  Symptom, immediately after any shape change and before the first re-run: the working tree and
  HEAD both hold old-shape recordings, so plain `pew stat` (auto mode) prints
  "no recorded benchmarks to compare" with no hint that recordings exist or why they were skipped.
  The same silence hits pinned mode and an A/B between two pre-change refs. (`pew status` is
  unaffected — it reports `stale (format)` per benchmark.)

  The *gating* arm of this symptom is fixed (spec §10.1, INV-10): `pew stat
  --fail-on-regression` now exits `2` instead of `0` when nothing was compared, and the
  empty-comparison line names the cause for every *inventoried* recording. What remains open here
  is the visibility arm: a recording dropped by `IsRecordingShape` *before* inventory contributes
  nothing to that diagnostic — the cause reads "no recordings on either side" even though old-shape
  recordings exist on disk / at the refs.
- **`pew gc`** (`cmd/pew/gc.go`, both `IsRecordingShape` filters): a shape-failing recording is
  never removed and never reported. Combined with the stat silence, a recording whose benchmark was
  since deleted from source is fully orphaned: no command surfaces it and no command will ever
  remove it.

## Resolution

Surface every shape-failing recording instead of skipping it silently: in stat, warn per dropped
path (same wording as the existing per-side stale-format warnings) or inventory the key and let the
per-side checks warn; in gc, treat a shape-failing recording for a benchmark absent from source as
removable (it can never be reused — regeneration is the only path) or at minimum report it. Add an
A/B test with both sides in an older shape and a gc test over an old-shape recording asserting the
warning/removal.
