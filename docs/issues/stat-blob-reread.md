# `pew stat` parses every historical recording twice

Lands: when `pew stat` inventory or side-reading code is next changed

## Fault (efficiency)

`cmd/pew/stat.go`: `addRefInventory` calls `readSide` on each candidate path to validate
`store.IsRecording` before admitting its key; the main comparison loop then calls `readSide` again
for the same (ref, key), reading and parsing the identical git blob a second time. Every historical
recording is materialized and parsed twice per invocation; with two historical refs (A/B mode) that
is four blob reads for two useful parses.

## Resolution

Cache parsed results keyed by (ref, path) — either a memo inside `readSide` scoped to the
invocation, or have `addRefInventory` retain the parsed results it already validated and hand them
to the comparison loop. Pure de-duplication; no behavior change.
