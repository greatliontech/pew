# Evidence model and bottom-up refactor proposal

This design rationale derives the measurement and reuse boundaries for pew and
its shared gofresh substrate. The canonical specs define the requirements; this
note explains the evidence-handling architecture and its dependency order. It
does not authorize a weaker reuse claim.

## Recommendation

Settle one end-to-end evidence contract before adding observed reuse, comparison
policies, or profile companions. Preserve the components that already satisfy
that contract, and rebuild the interfaces that currently blur facts, assertions,
and decisions.

The default should remain conservative: an unavailable premise is unavailable,
not an assertion silently manufactured by a convenient constructor. Do not add
another user-facing assertion switch merely to make the next integration work.
Existing deliberate purity declarations remain a separate, explicit trust lane.

The redesign crosses pew and gofresh. Gomutant and stipulator need coordinated
producer adapters because they consume the same observation facade. This is not
a proposal to rewrite their unrelated mutation or requirement engines.

## What the tool is actually claiming

A benchmark recording is an immutable historical observation, not a prediction
that the next execution will take exactly the same time. Four questions belong
to different judgments:

1. **Was this a usable measurement?** Did the intended subject execute, did its
   contributing process complete, is its result stream complete, and did the
   samples satisfy the measurement protocol?
2. **May it be reused under the current model?** Do its source/build/runtime
   guards and the required observation evidence still establish eligibility,
   under explicitly stated environmental assumptions?
3. **May these recordings be compared?** Are their workload, metric definitions,
   provenance and selected condition policy compatible, and is requested
   comparison coverage sufficient?
4. **What does a profile explain?** Which diagnostic execution produced the
   samples, which workload and build it represents, and what attribution the
   available symbols and mappings support?

`valid`, process exit zero, a matching file digest, a complete comparison, and a
useful profile are not interchangeable answers. A freshness judgment also does
not certify freedom from scheduling noise or good experimental conditions.

## Confirmed contract mismatches

### Completion and outcome agreement are conflated

Gofresh's observation contract requires both normal process termination and
evidence that behavior-affecting admitted-operation outcomes agreed with their
guarded values. Its testlog model explicitly omits return values, byte counts,
and errors.

Process health, identities, environment, brackets and declarations do not establish
operation-outcome agreement. A producer facade needs an independent outcome
premise rather than supplying a combined assertion after checking only health.
The shared construction boundary binds a completion receipt and analysis-issued
support to one execution; identity-only guard data remains a separate claim.

An allowed error path exposes the missing implication: a benchmark ignores a
failed or partial file read, still emits its samples and exits successfully, and
the parent subsequently reads the unchanged file successfully while finalizing
the observation. Stable content and metadata do not establish successful delivery
of those bytes to the child. This is a source-supported counterexample, not a
claimed reproduced field incident. It needs no file mutation-and-restore interval,
so the existing coherence exclusion does not settle it.

The scope of the finding matters:

- Pew currently selects no observability proof for recording reuse. The proposed
  automatic file-read lift would expose the gap; this finding does not demonstrate
  a current false-valid in that conservative path.
- Gomutant and stipulator already use observed proof and the same facade. Their
  health and test-verdict checks do not independently record operation outcomes.
  This establishes a missing premise under the stated contract, not proof that
  every cached result or any particular historical result is wrong.
- The static observability proof establishes which effects can be represented.
  Attachment, seals, and later validation preserve supplied evidence; they cannot
  create a fact the producer never established.

### Fingerprint serialization has two owners

Gofresh requires callers to store its native fingerprint record form rather than
invent another encoding. Pew's contract and implementation independently map the
fingerprint into benchmark configuration lines.

Adding observation fields to that parallel mapping would extend the discrepancy.
The repair is one canonical fingerprint payload owned by gofresh, carried by pew's
artifact envelope. Pew can still own its result rows, commit, dirty provenance,
conditions, ledger, and profile relationships. Fields needed for display or
comparison should be projections of the admitted payload rather than independent
persisted copies that need to agree.

### Ownership is distributed across interfaces

An analysis view owns source facts but also accumulates captured-proof flags,
observation attachments, and a validation seal. A consumer must understand that
calling a capture operation changes the later validation obligation. Per-arm
transactions therefore require correctly narrowed sibling views.

Pew also distributes invocation policy through vouch globals and restores a row
through several maps and admission paths. These are not evidence that every
current path is wrong. They make the next cross-cutting change difficult to prove
end to end and encourage local fixes to one surface at a time.

## Derived evidence contract

### Facts, proofs, assertions and absences remain distinct

Every claim has a named producer and a support class:

| Claim | Appropriate support |
|---|---|
| Selected source/build/environment | Coherent analysis snapshot and build provenance |
| Intended process and subject completed | Process receipt plus recognized harness completion and sample checks |
| An identity was observed | Observation stream |
| A guarded value was stable across the run | Matching pre/post bracket and environment snapshot, within declared coherence assumptions |
| Effects are covered by the observation model | Compatible engine observability proof |
| Operation outcomes agreed with guarded values | Independent outcome evidence or a specifically identified caller assertion |
| A result can be reused | A decision derived from all required claims and current guards |

The outcome row is a real design obligation. A second boolean named `verified`
would only move the unsupported assertion. General instrumentation and narrower
static restrictions are candidate mechanisms, not promises that either already
works. A capability experiment must establish a useful supported subset, including
failure paths, before automatic file-read reuse is enabled.

All meaningful guarantees are relative to a declared model. Mutation exclusions,
filesystem assumptions, and trusted author assertions must be stated at that
boundary. The implementation cannot quietly narrow a strong contract to whatever
the available telemetry happens to observe.

### A result-contributing run is the transaction unit

The lifecycle is:

**admitted request → immutable analysis/build facts → per-subject execution →
receipt and observations → evidence judgment → validation → atomic publication**.

- Several subjects can share immutable preparation facts.
- Each measured arm owns its receipt, observation attachments and publication.
- A sibling-local failure does not discard an otherwise publishable arm.
- Shared preparation facts impose shared validation dependencies: source or HEAD
  drift refuses every pending publication that depends on the invalidated premise.
  Completion alone does not authorize publication. Already-published recordings
  are retained and judged against the changed tree; they are not silently deleted.
- Failed validation preserves the prior recording.
- A separate diagnostic execution cannot supply missing facts about an earlier
  timed execution. Instrumentation overhead must not be silently presented as
  uninstrumented benchmark performance.

### Historical facts are not invented; applicability can be proved anew

Decoding, merging, checking, and persisting do not retroactively strengthen
historical execution facts or their support class. A hash agreement does not
turn identity-only capture into outcome evidence. A healthy process does not
repair missing subjects or lost samples. An author assertion stays identified as
an assertion even when a seal protects its serialization.

Derived applicability may change when an explicit proof justifies the change.
Pew's inert test-variant extension is an existing example: establish the permitted
ledger extension, refresh the derived compartment evidence, and recheck every
remaining guard while leaving measurement rows unchanged. Native decoding does
not itself perform or authorize this transformation. The strength of that
applicability remains the declared model's strength, not a claim of identical
future timings or identical profile addresses.

### Admission is shared; policy is explicit

One admitted recording feeds run selection, status, comparison and explanations.
Admission owns decoding, structural validity and internal consistency. Later
decisions explicitly name their scope: current-tree reuse, historical comparison,
measurement conditions, or profile compatibility.

Unavailable evidence, stale evidence, invalid artifacts, interrupted work and
operational failures remain distinct. A gate does not report success merely
because every unjudgeable candidate disappeared from its input set.

## Proposed ownership boundaries

**Gofresh owns the shared evidence language:** coherent snapshots, source and
effect analysis, guards, observation claims, compatibility rules, canonical
fingerprint encoding, and the derivation of freshness decisions. Its producer API
must expose the actual premises required to construct completion-bearing evidence.

**Pew owns the experiment:** workload selection, build/execution arrangements,
sample completeness, benchmark-specific health checks, condition policy,
per-arm transactions, storage, statistical comparison and profile companions.

**Gomutant and stipulator own their result semantics:** mutation/test verdicts,
oracle or witness membership, and their execution-health rules. They consume the
same evidence language and do not independently reinterpret observation completion.

Within pew, one invocation-owned value carries admitted environment, store/vouch
selection, preparation resources and policy. The CLI translates arguments and
renders decisions. It should not assemble evidence from process-wide mutable
policy or choose validity behavior implicitly through call order.

Separate immutable analysis facts from mutable producer transactions. A
transaction can reference a shared snapshot, but its subject membership,
attachments and publication state are its own. This is the conceptual boundary
to establish before choosing concrete API names or file layout.

## What to preserve

- The standard benchmark text format and imported statistical algorithms.
- Exact numeric edge-case handling and explicit empty-comparison failure.
- Confined store paths and atomic replacement of completed units.
- Shared subprocess containment and immutable environment snapshots.
- Source/build/runtime drift guards and fail-closed unknown-effect handling.
- Explicit author purity declarations, kept separate from verified observations.
- Regeneration-only recovery: old/corrupt known recordings can be remeasured,
  prior bytes survive failed measurement, and incomplete status is nonzero.
- Existing regression witnesses, strengthened where their oracle does not cover
  the claim. A green test of facade assembly is not an outcome-evidence test.

These components provide enforcement worth retaining. The architectural change
is primarily about who can construct, combine, serialize and interpret evidence.

## Dependency order for the refactor

1. **Settle the shared claim model.** Reconcile completion, outcome agreement,
   model assumptions and serialization in the canonical contracts. Map each
   claim to its current producer and an independent witness. Audit already-active
   observed consumers before making a fleet-wide cutover.
2. **Establish a sound observation capability.** Exercise successful reads,
   partial/error outcomes, missing logs, startup effects, source movement and
   incomplete child processes. Unsupported cases remain conservative. Separate
   genuinely established evidence from explicit assertions at construction.
3. **Replace the persisted evidence boundary.** Use the native gofresh record in
   one pew envelope and decode it once. Derive display/comparison projections
   from that admitted record. Use a coordinated clean format cutover; retain
   regeneration rather than a format-2 compatibility layer. The deployment scope
   is fleet-owned projects, so external legacy-support commitments do not constrain
   this design. A reset to v1 belongs after the schema is settled, not during each
   intermediate step.
4. **Unify the invocation and producer transaction.** Bind environment, vouches,
   subject selection, receipts, capture and validation to their proper lifetimes.
   Adapt run/status/stat and the other shared-library consumers together at each
   contract boundary. Preserve per-arm persistence and cancellation behavior.
5. **Build comparison and profile capabilities on that base.** Give coverage and
   condition compatibility explicit policies. Bind diagnostic profiles to their
   actual producing executions and sample kinds. Then add attribution, A/B
   comparison and noise-floor handling without inventing a parallel evidence model.

Capability feasibility must distinguish matching hashes from justified reuse,
complete output from parseable prefixes, and recorded measurements from current
validity. Claims about operation outcomes need failure-sensitive evidence;
success-only examples cannot establish them.

## Adoption boundary

This proposal recommends a shared evidence-spine refactor and a conservative
default, not a broad rewrite or an additional assertion knob. The observation
mechanism still needs a bounded feasibility experiment; an unproven mechanism
cannot be promised as automatic reuse for every file-reading benchmark.

The next implementation roadmap should be derived from the reconciled contract.
The remaining feature roadmap is input to that derivation, not authority to skip
an evidence prerequisite. Existing observed records need an explicit compatibility
decision at cutover; old assertions cannot silently become newly verified facts.

## Contract and implementation anchors

- Pew [canonical contract](../specs/spec.md), especially recording format,
  observation evidence, single-subject execution and comparison coverage.
- Gofresh [overview](https://github.com/greatliontech/gofresh/blob/main/docs/specs/overview.md):
  observation conjunction/lifecycle and fingerprint data/record contracts.
- Gofresh [runtime-input contract](https://github.com/greatliontech/gofresh/blob/main/docs/specs/runtime-inputs.md):
  completed observations, value binding, coherence and producer facade.
- Gofresh `runtimeinput.WithCompletedProcess`, `ProducerFrame.Observe`,
  `View.AttachObservation`, `View.Sibling`, and `Fingerprint.MarshalJSON`.
- Pew [`internal/run/observe.go`](../../internal/run/observe.go),
  [`internal/run/run.go`](../../internal/run/run.go),
  [`cmd/pew/run.go`](../../cmd/pew/run.go), and
  [`cmd/pew/vouchfile.go`](../../cmd/pew/vouchfile.go).
- Other producer adapters: gomutant `internal/engine/run.go` and
  `freshness.go`; stipulator `internal/backends/golang/observe.go`,
  `freshness.go` and `publish.go`.
