# Derived-state discard and recomputation controls

Lands: Gofresh docs/plans/tooling-redesign.md 7.8.

`REQ-pew-derived-state` in spec §13 requires complete dependencies:
source membership/content, selection, derivation, and the toolchain-content
and admission dependencies of each claim. It explicitly rejects the exclusive
tuple `(commit, toolchain, buildconfig)`. The earlier proposal to vary any input
outside that tuple while expecting an unchanged key was therefore unsound:
some such inputs are relevant dependencies.

Completed recomputation must agree with valid memoized facts. Budget-dependent
availability is separate: a cold miss can exhaust a budget that a warm hit
satisfies, but it cannot acquire stronger authority or backfill historical
support. The witness should compare semantic facts and verdict authority,
not require byte-identical diagnostics or equal availability after a budget cut.

The bound `TestStatRecordingPredatingDynamicStateKeyIsStale` and
`TestStatusRecordingPredatingDynamicStateKeyIsStale` cover the strategy-break
authority arm, not an actual cache-discard/recompute control. The declared
witness gap already exists at
`.stipulator/gaps/pew-derived-state.textproto`; the prior claim that no gap
could represent the obligation is obsolete.

The remaining consumer witness must exercise the shared Gofresh cache boundary
through Pew's verdict paths: compare cold, warm and discarded-cache completed
answers, preserve valid-hit controls, and vary same-version selected content,
source coordinates and relevant versus irrelevant inputs. Pin budget-cut
availability separately and preserve historical non-upgrade. Reconcile the
existing gap against those witnesses rather than declaring it resolved from a
dependency bump. Shared derivation owns the cache; no second Pew closure store
is required.
