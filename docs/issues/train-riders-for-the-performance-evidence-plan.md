# Handoff: the train's pew riders for the performance-evidence plan

Lands: performance-evidence plan chunk 7, with the later-chunk items
named below landing at their stated chunks.

The cross-tool train (gofresh docs/plans/cross-tool-train.md) held pew
chunks whose derivations live in that plan's entries and ticks — a
place this plan's machine never reads. The fifth re-audit band (train
chunk 319, 2026-10-06) dissolved the train's pew tail into this plan
and moves every rider here. Each item is a derived fact, not a wish;
its source tick is named so the derivation can be read.

## At chunks 7 and 8 — evidence encoding and recorded-observation freshness

- One verdict path: `checkOne` is test-only; `checkPackage`,
  `verdictForRecs` and `inertGrownRecheck` beside it; admission
  decodes each row three times (admission.go, status.go) — one
  shared admission/verdict path, pins moved never deleted, preserving
  REQ-pew-admission's order and the strategy rung on working-tree
  sides only (`verdict-path-consolidation`,
  `strategy-stale-arms-flipped-valid-without-rerecord`: the explain
  row per validity key rides along).
- The vouch set as four process-wide globals (status.go,
  vouchfile.go) whose correctness depends on call order (044f7d8
  itself reordered stat's resolveVouches): one threaded value with
  engine construction, preserving REQ-pew-vouch-source (file ∪
  flags; flags never remove a vouch).
- The canonical fingerprint boundary lands at 7: settle its envelope
  and clean format cutover before emitting it, preserving the current
  regeneration-only policy and making the remeasurement consequence
  explicit. Missing historical evidence is never filled in on read.
- The two covered gaps with no scheduled work
  (`.stipulator/gaps/pew-closure-noncall`, `pew-mutable-local`): the
  spec's anchors (a const flip, a struct-field change, an embed edit;
  a local-replace edit) land at 8.4's movement verification.

## At chunk 9 — comparison policies

- §10.1's unqualified "its magnitude clears a threshold" beside the
  three REQs 044f7d8 added, and the null-delta rule stated three
  times (REQ-pew-zero-baseline, REQ-pew-delta-representation, §12):
  one statement at 9.1.
- The metric set spelled four times (stat.go `knownUnits`, compare.go
  `higherIsWorse` and `unitOrder`, a test's hand list): one registry
  on 251's pattern at 9.2, preserving §10.1's sec/op gate default;
  `audit-note-renderers-one-grammar` lands here.

## At chunk 10 — profile companions

- Every companion key goes through `RecordingKeys` (REQ-pew-key-set's
  closed set); `profile-capture-attribution` lands at 10/11.

## At chunk 11 — A/B

- ab's seven bare `exec.Command` git stages break REQ-pew-interruption
  ("any stage on the verb's path"): the containment, with completed
  units preserved on interruption; `ab-out-multi-package` lands here.

## At chunk 12

- Per-arm noise floors over same-closure recordings are sound on the
  package closure today (gofresh 102 only refines them); the `--json`
  parity (the train's 177) at 12.1/9.2.

## What the train keeps (after this plan closes)

pew 320 (the residual bump: the releases this plan does not take,
300's lint pew half, a resident ceiling), 252r (seven containment
spellings, two module resolvers, the vestige set — `ExecuteBinary`,
`run.Execute`, `gitblob.State` — the stretch literals, the
declared-benchmarks rule, stat's partial pkgMeta; audit 335 struck
`recordingFromPath` and `equalExcept` (deleted) and `checkOne` → 8.3,
the writer census → 7.2; `isPewRecording` is live), 232r (`--explain`'s
stream, REQ-pew-derived-state's payload list, the REQ home rule with
its two new pairs; the `pew-format-invalid` key → 7), 276r (§9's
corruption grammar, the conditions grammar — the ledger round trip →
7.3,
the pin ladder, the eleven uncited ids, the self-oracle pins in
guidance_test.go and knobs_test.go, the six bare `gofresh.New()`
engines in tests).

## Band R6 inputs (2026-10-07) — for the performance-evidence plan's owner

Findings the sixth re-audit band routes here rather than to a train chunk:

- Both consumers' specs still claim a "completed observation" the
  facade builds the old way: `WithCompletedProcess` supplies outcome
  agreement with no input that establishes it, while gofresh 7b08f75's
  REQ-inputs-producer-premises/-outcome-method require the completion
  receipt and the outcome support as separate premises; stipulator's
  REQ-evidence-witness-freshness and gomutant's execution.md bind values
  through it (5.2/6.1's triage of the adapters). The train restates no
  premise meanwhile (270, 269 told at open).
- 3361645's outcome derivation is per subject; stipulator attests the
  package-process model (one process runs every selected test) and
  gomutant's oracle processes run under a parent-composed environment
  (GOMEMLIMIT, GOMAXPROCS); pew measures under its pinned snapshot — the
  contributing-subject set and the inherited environment the derivation
  binds to (5.2's unticked bullet).
- ObservationRTA @41 versions `closure.Observability.OutcomeMethod`, a
  field no recorded proof or fingerprint carries — every consumer's
  recorded proof is refused once at its bump to version a disposition
  that did not move; gofresh 203 splits the memo version from the
  recorded strategy; if @41 is your "versioned judgment",
  REQ-closure-outcome-inventory should say recorded proofs are what it
  versions.
- §5's three stale format sentences (:657 vs :139/:1201; :189; the
  artifact-format anchor vs the stale(format) inventory) → 7.1; §7.8's
  two normative statements (no invented support vs the ingest "with the
  completed-process options") → 6.2; guidance-purpose-column → 12.2.
- The four gofresh gap records citing this plan's chunks 5/6/8 retarget
  when it closes; the inputs-outcome-method gap's reason ("no
  outcome-support capability is claimed") is stale now that
  `gofresh/immutable-environment@1` exists.
- 335's chain-vocabulary finding (stipulator mints kinds into
  gofresh.Chain) is gofresh 241's, not this plan's.

### Report to the evidence-model owner: stipulator 337.A (the roots probe under the owned runner)

Filed 2026-10-07 by the train session before implementing (a report, not an approval gate — the user's 2026-10-07 ruling).

**What**: stipulator's observation frame (internal/backends/golang/observe.go, `runtimeinput.ProducerIngest`) carries no `Runner` and no `Roots`, so gofresh's roots probe (`go env -json GOROOT GOMODCACHE GOCACHE` in the package directory) spawns under a zero `gotool.Runner` — outside stipulator's owned boundary, unobserved, unmemoized (one spawn per package observation). REQ-go-owned-processes says every go child but the loader's runs through the runner; the 290 rider that passed the memo was dropped at 6ec5cfd.

**Change**: observeProcess passes `Runner: <the owned runner>, Roots: <one runtimeinput.Roots per judged operation>` — the Roots instance minted beside the per-operation toolchain sampler (policy discovery, the resolver child's load, the Served backend) and held on the capture run. The memo is gofresh's (`gotool.MemoKey`: directory coordinate + normalized env less PWD; a cancelled probe never memoized).

**Boundary classification**: plumbing on the spawn side — it changes WHO spawns the probe and memoizes its answer, not what the observation classifies, its manifest, its digest, or the record's wire form. The roots the probe answers are the same bytes from the same `go` under the same environment; the memo returns the first answer for an identical key within one judged operation, which gofresh's own facade already guarantees for gomutant and pew (281.D). No observation-completion, fingerprint-serialization, attachment/validation, or observed-reuse-admission code is touched.

**Pin**: a spawn-observing pin over the execute fixture — the probe reaches the owned runner's Prepare hook, once per (directory, environment) across a group's package observations.

**Asks**: none; if the evidence model's next contract moves the roots probe (e.g. roots as a declared input of the observation rather than a probe), say so and 337.A's memo becomes that contract's — the per-operation holder is the shape either way.

**Rebase note (gofresh e10bad5, 2026-10-07)**: your `feat(runtimeinput)!` keeps `ProducerIngest.Runner`/`.Roots` (producer.go:120/127) while adding the Completion/Outcome premises; stipulator pins v0.109.6 and sees neither until its bump (290r). 337.A edits the same `ProducerIngest` literal in observe.go that PE 6's adapter migration will edit — two fields added now, which the migration keeps; the premises are the migration's.

### Report to the evidence-model owner: gomutant 324 pins gofresh v0.111.0 (the last release before the producer protocol)

Filed 2026-10-08 by the train session (a report, not an approval gate).

**What**: gomutant's bump behind gofresh 265/266/274/279/292/300 (chunk 324) pins gofresh at v0.111.0 (3361645, ObservationRTA @41) — NOT v0.111.1 (e10bad5: `ProducerIngest` loses `IncompleteReason` and gains the completion receipt + outcome support; `Observe` requires analysis-issued support, `ObserveInputs` is the identity-only form) nor v0.112.x (e7df801: `ObserveInputs` returns the reason). gomutant compiles unchanged at v0.111.0; v0.111.1 breaks one site (internal/engine/run.go's ingest literal).

**Why**: the choice between `Observe` with zero support (every record incomplete) and `ObserveInputs` (identity-only, never reusable) IS your plan's chunk 6.2 triage ("Migrate gomutant's baseline and transformed-executable observations without borrowing unsupported evidence from another execution model"; gofresh docs/issues/outcome-mutation-oracle-evidence.md lands there). 324 does not pre-empt it; the migration to v0.111.1+ is 6.2's, on your side.

**Boundary classification**: 324's other change sets (the exemption-clause re-key per gofresh 292, the version grammar, Runner.List / the contained git runner / one roots memo per judged run, the guidance riders, the resident clause pointer, the two project invariants as bound requirements) touch no observation-completion, fingerprint-serialization, attachment/validation, or observed-reuse-admission code. The roots memo (one `runtimeinput.Roots` per judged run on `OracleBounds`) is the 281.D/337.A plumbing shape: WHO spawns and memoizes, not what is classified.

**Asks**: none. Every fleet record re-measures once at this bump (@41); a second re-measure at 6.2 is expected and recorded, not deferred.
