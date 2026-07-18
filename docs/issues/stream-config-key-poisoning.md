# Interleaved stdout lines can poison recording configuration keys

Lands: when a stream-derived configuration key is observed in a real recording, or before pew
targets a package whose benchmarks (or their dependencies) log `key: value`-shaped lines — a
lowercase, colon-terminated first word — to stdout

## Fault (latent, adjacent to the resolved parse-interleaving fix)

`benchfmt.Reader` treats any stream line whose first word is lowercase, space-free, and
colon-terminated as a **file configuration line**. A benchmark dependency logging in that shape
(`raft: appending entries`, `warning: slow disk`) therefore silently becomes a configuration key on
every subsequent parsed result, and `pew run` records it into the affected recordings.

Consequences:

- The recording remains benchfmt-valid (INV-3 holds), but carries transient log text as durable
  configuration.
- `pew stat` groups rows by `.config` (with only pew's own keys projected away, spec §10.1): a
  junk key present in one run and absent from the baseline fragments the grouping, so a benchmark
  can silently fall out of comparison (one-sided skip) because a dependency logged once.

Spec §5 refuses only Pew-owned/reserved keys; it is silent on which other stream configuration
keys are recordable. The dragonboat/capnslog format observed in the resolved interleaving fault
(`2026-07-18 03:26:29 I | dragonboat: ...`) starts with a digit and does **not** trigger this —
the fault needs a bare `key:`-prefixed logger — so this is filed, not fixed: the disposition
(whitelist the toolchain's four keys, drop-and-warn foreign keys, or refuse fail-closed) changes
what a recording may contain and needs its own spec wording, including whether deliberately
emitted custom benchmark configuration is a supported use.

## Reproducer

A benchmark whose body runs `fmt.Println("raft: x")` before its measurement: the parsed results
gain a `raft` configuration key, and the stored recording carries `raft: x`.
