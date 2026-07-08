# `--label` on `pew status` (and stat's staleness warning)

Lands: when labeled-variant recordings see real use, or the status CLI surface is next extended

## Gap

`cmd/pew/status.go` hard-wires label `""` into `checkOne`, so a labeled recording
(`BenchmarkFoo.cgo.txt`, spec §6 `--label`) can never be verdict-checked: `pew status` reports the
benchmark `unrecorded` even though a labeled recording exists, and `run --stale` on the labeled
variant works while plain `status` cannot show why. `pew stat`'s working-tree staleness warning
already threads `sc.label` through, so the asymmetry is status-only.

`pew run` and `pew stat` both expose `--label`; `status` omitting it makes the labeled workflow
opaque in exactly the inventory-plus-verdict view the spec assigns to `status` (§12).

## Resolution

Add `--label <name>` to `pew status`, defaulting to `""` (the unlabeled recording), passed through
to `checkOne` — mirroring `stat`'s flag. Optionally: with no flag, also list labeled recordings
present in the store as additional rows, since `status` is the inventory view; decide whether that
default is noise or signal when landing.
