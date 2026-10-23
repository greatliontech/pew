# Issue docs — deferred follow-ups

Tracked deferrals carrying a `Lands:` trigger. On resolution, the load-bearing rationale is promoted
inline to the spec / a test, and the doc is deleted (git holds history) — per the Issue triage
close-out convention.

No open issues.

## In-spec upgrade paths (tracked inline, not here)

These five alternatives remain conditional obligations, not automatic mechanism
choices:

- VTA call graph (§7.4) — only after a demonstrated RTA precision
  shortfall and a witness separating relevant from spurious reachability.
- Skipping standard-library body loads (§7.4) — conditioned on measured cost
  and the escape/soundness proof.
- Per-declaration hashing into cache dependencies (§7.7) —
  conditioned on relevant-edit invalidation and irrelevant-edit reuse evidence.
- Persistent closure memo (§6) — owned by the shared derivation
  layer under complete generation identities, never a second Pew store.
- Same-identity sample merge (§6) — only for a real augmentation
  workflow after lineage, conditions and sample-independence obligations are
  established; equal identity alone licenses no merge.
