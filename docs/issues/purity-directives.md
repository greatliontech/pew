# Durable impure directive (`//gofresh:external`-style)

Lands: when `--impure` sees enough use that re-specifying it per run is friction, or a known-external
benchmark's marker needs to travel with the code through clones/CI.

## Context

The purity escape hatch's *pure* half is durable now: `//gofresh:pure` on the benchmark declaration
is honored by the shared gofresh engine and specced at §7.5. The *impure* half has no durable form —
`--impure <bench>` (spec §7.3) remains a per-invocation CLI flag recorded as `pure: false`.

## The exploration

A durable external-state directive (gofresh would own the grammar, mirroring `//gofresh:pure`) would
let a known-external benchmark carry its always-re-run marker in code: self-documenting, reviewed,
no per-run flag memory. It belongs upstream in gofresh so every engine consumer honors it, with pew's
`--impure` remaining the per-run override.

## Open questions

- Whether gofresh models "impure" at all: today unverifiability suppression is the engine's only
  purity input; an "always unverifiable" assertion is currently a pure caller-side concept
  (pew's `applyPurity`).
- Precedence with `//gofresh:pure` on the same symbol (likely: external wins, mirroring the CLI
  precedence at §7.5).

## Resolution

On landing: spec the directive in gofresh, honor it in the engine or keep it caller-side by
documented choice, promote this rationale inline, then delete this doc — git holds history.
