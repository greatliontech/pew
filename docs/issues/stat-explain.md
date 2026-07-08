# Implement the spec-reserved `pew stat --explain`

Lands: when guard-failure opacity bites in real use (a `stale`/`unverifiable` verdict whose cause is
not evident from the one-word reason)

## Gap

Spec §12 reserves `--explain` — "a detailed guard/input explanation view over `pew-closure` and
`pew-runtime*`" — and nothing implements it. Today a verdict surfaces only the first failing guard
key (`stale (pew-runtime)`), which is not actionable: the user cannot see *which* observed input
moved, what the manifest contains, or which guard values differ.

All the data is already in-band (§5): the recorded guard values, the decodable
`pew-runtime-inputs` manifest, and the recomputable current-side values.

## Resolution

Add `--explain` (to `stat` per the spec's reservation; consider `status` too, where verdicts
actually surface): per benchmark, print each guard's recorded vs current value, and for a
runtime-input mismatch decode the manifest and re-hash inputs individually to name the moved
input(s). Environment values stay hashed — print names and hash-equality only, never clear text
(§7.8).
