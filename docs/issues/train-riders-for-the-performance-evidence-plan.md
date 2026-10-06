# Handoff: the train's pew riders for the performance-evidence plan

Lands: performance-evidence plan chunk 2 (the gofresh bump), with the
chunk-4, -5 and -6 items named below landing at those chunks.

The cross-tool train (gofresh docs/plans/cross-tool-train.md) held pew
chunks whose derivations live in that plan's entries and ticks — a
place this plan's machine never reads. The fifth re-audit band (train
chunk 319, 2026-10-06) dissolved the train's pew tail into this plan
and moves every rider here. Each item is a derived fact, not a wish;
its source tick is named so the derivation can be read.

## At chunk 2 — the gofresh bump (the train's 291)

- **Soundness, pinned as a fact:** the measurement spawn carries no
  Quit arm. gofresh's `gotool.Containment` sends SIGQUIT before
  SIGKILL; a SIGQUIT to a `go test` benchmark dumps goroutine stacks
  into the measured stream, which §9 reads as splice evidence
  (REQ-pew-sample-completeness). `runCommand`
  (internal/run/run.go) moves onto `Runner.Program` only with the
  Quit arm absent, and a pin witnesses the absence (train tick
  5ede52e, audit 288's rider on 291).
- `provenance.go` builds the provenance as a struct literal and
  discards the checked sample (`_, err := …Check`): both move to
  `gofresh.NewToolchainProvenance` and read the returned sample
  (gofresh 281.C, 2dbbc6f; a sampler is bounded to one judged run —
  REQ-fresh-toolchain-skew's consumer obligation).
- The row projection (`fingerprintFromConfig`, status.go) validates
  through `gofresh.Fingerprint.Validate` (gofresh 274, 0063f46).
- pew's own `go list` calls (status.go, stat.go, ab.go) take
  `Runner.List` — a pipe-hold salvage is refused outright
  (`ErrListingRefused`; gofresh 281.B, 53b9e1d). `ProducerIngest`
  takes one `runtimeinput.Roots` per verb invocation (281.D, 921bd84).
- `Observation.Attribution` (gofresh 279, 0a3fccb) is read wherever
  a refusal's reason is rendered; `runCommand`'s 2 s `WaitDelay`
  salvage follows gofresh's salvage rule (`gotool.Salvaged`).
- The bump target: whatever gofresh has released. gofresh 292 (the
  in-module refused path spelled module-relative — a clause re-key
  of exemption records), 297 (the dst-selection walk) and 300 (the
  guidance lint's gofresh half) head the train's lane; a bump taken
  before they release is followed by the train's pew 320 (the
  residual bump with 300's pew half: the five derived defaults 275
  spelled in prose).
- gofresh/resident, the content-keyed toolchain audit (310) and the
  version grammar have no pew reader today — nothing to adopt.
- The chunk's close-out reinstall (2.3) also drops the two `mcp.log`
  lines from `.gomutant/.gitignore` (gomutant 312 moved the exit log
  out of every served tree) — after this machine's gomutant is
  reinstalled at c6a9811 or later, never before.
- `environment-normalized-once` (docs/issues) lands here: the chunk
  touches every normalization site.
- The fleet's CI/release gate (gofresh 48bbe3a; the train's doctrine
  2026-10-02: every consumer adopts it at its next chunk — gomutant
  323 and stipulator 321 charter it at this band): release.yaml on
  `workflow_run` of a successful CI for a same-repository push,
  serialized, SEMREL_BRANCH=head_sha, a superseded commit releasing
  nothing; a records job (stipulator compile + the bindings view
  failing on any non-current row); next-rc its own workflow. pew's
  release.yaml still releases on every push to main. This plan's 2.3
  or 8.3 close-out is the place.

## At chunk 3 — malformed and legacy recordings

- `loader-refuses-pre-format-3-ledger-lines-remedy-circular`: the
  refusal §5 and REQ-pew-artifact-format mandate names a regenerating
  operation (`pew run`) that loads the store first and so can never
  run — an internal contradiction the train wrote at its chunk 230
  (c28ebd4). The anchor amendment is this chunk's spec-first step.

## At chunk 4 — recorded-observation freshness

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
- The format bump 127 assumed is DERIVED, not assumed: §5's
  omittable class reads an absent row as today's behaviour, so the
  two observation rows may need no format move; if a bump is owed,
  232's re-class announcement ships in the same change set (every
  bump turns the whole fleet store `stale (format)`).
- The two covered gaps with no scheduled work
  (`.stipulator/gaps/pew-closure-noncall`, `pew-mutable-local`): the
  spec's anchors (a const flip, a struct-field change, an embed edit;
  a local-replace edit) land at 4.3's movement verification.

## At chunk 5 — comparison policies

- §10.1's unqualified "its magnitude clears a threshold" beside the
  three REQs 044f7d8 added, and the null-delta rule stated three
  times (REQ-pew-zero-baseline, REQ-pew-delta-representation, §12):
  one statement at 5.1.
- The metric set spelled four times (stat.go `knownUnits`, compare.go
  `higherIsWorse` and `unitOrder`, a test's hand list): one registry
  on 251's pattern at 5.2, preserving §10.1's sec/op gate default;
  `audit-note-renderers-one-grammar` lands here.

## At chunk 6 — profile companions

- Every companion key goes through `RecordingKeys` (REQ-pew-key-set's
  closed set); `profile-capture-attribution` lands at 6/7.

## At chunk 7 — A/B

- ab's seven bare `exec.Command` git stages break REQ-pew-interruption
  ("any stage on the verb's path"): the containment, with completed
  units preserved on interruption; `ab-out-multi-package` lands here.

## At chunk 8

- Per-arm noise floors over same-closure recordings are sound on the
  package closure today (gofresh 102 only refines them); the `--json`
  parity (the train's 177) at 8.1/5.2.

## What the train keeps (after this plan closes)

pew 320 (the residual bump), 252r (seven containment spellings, two
module resolvers, the vestige set — `ExecuteBinary`,
`recordingFromPath`, `run.Execute`, `gotool.RunIn`, `gitblob.State`,
`checkOne`, `equalExcept`'s unused parameter — the stretch literals,
the writer census, the declared-benchmarks rule, stat's partial
pkgMeta), 232r (`--explain`'s stream, the `pew-format-invalid` key,
REQ-pew-derived-state's payload list, the REQ home rule), 276r (§9's
corruption grammar, the ledger round trip, the conditions grammar,
the pin ladder, the eleven uncited ids, the self-oracle pins in
guidance_test.go and knobs_test.go, the six bare `gofresh.New()`
engines in tests).
