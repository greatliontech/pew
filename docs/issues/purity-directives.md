# Durable purity directives (`//pew:pure` / `//pew:external`)

Lands: when CLI purity flags prove insufficient — i.e. once `--assume-pure` / `--impure` see enough
use that re-specifying them per run is friction, or a benchmark's purity needs to travel with the
code through clones/CI.

## Context

Class-B external-dependence detection (spec §7.3) is best-effort. Two user overrides ship as **CLI
flags**, recorded as provenance:

- `--assume-pure <bench>` → records `pure: true` (§7.5) — suppress Class-B for a benchmark the
  author knows is perf-pure (e.g. reads a fixed fixture).
- `--impure <bench>` → forces `unverifiable` — for a known-external benchmark detection missed.

The flags are imperative: re-specified each run, living in shell history rather than with the code.

## The exploration

A **durable code directive**, read during the analysis pass, would be nicer:

- `//pew:pure` above a benchmark ≈ `--assume-pure` — travels with the code.
- `//pew:external` ≈ `--impure`.

Pros: durable (clones/CI carry it), self-documenting at the benchmark, reviewed in code review, no
per-run flag memory. Cons: requires comment parsing in the analysis pass; another directive surface
to spec and test.

## Open questions

- Directive syntax / namespace (`//pew:pure` vs a `//go:`-style form).
- Precedence when both a directive and a CLI flag apply to the same benchmark.
- Whether recorded provenance notes the source (directive vs flag) for auditability.

## Resolution

On landing: spec the directive grammar, add it to the analysis pass, promote this rationale inline
to that spec section (per Issue triage close-out), then delete this doc — git holds history.
