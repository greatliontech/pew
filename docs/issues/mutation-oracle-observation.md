# Incomplete mutation campaigns and subprocess coverage

Lands: Gofresh docs/plans/tooling-redesign.md 9.1.

Item 9.1 reconciles the exact residual scopes from performance-evidence 11.3
and 12. Authorized final execution belongs to 9.3, only after 6.6's
selection-coexisting records and 6.7's recovery/progress boundary. This ledger
authorizes no new campaign or benchmark. The old plan supplies scope, not order.

## Unresolved evidence boundary

Pew's full derived oracles include CLI integration tests that change working
directory and re-execute the test binary as a subprocess. A refusal such as
`relative runtime input after working-directory change: cmd/pew` is legitimate
unsupported cwd/evidence, not a Pew observed-capture defect. Execution kills,
reusable freshness evidence and a completed campaign are separate claims.
Do not weaken or exclude integration tests, or add purity vouches, to obtain a
reusable verdict or a passing campaign.

Gomutant's parent `-coverprofile` does not collect a self-reexecuted child's
counters. A mixed parent-coverage partition therefore cannot justify omitting
a mandatory subprocess testcase. The governing boundaries are gomutant's
`docs/specs/execution.md` (`REQ-exec-oracle-run`) and `docs/specs/results.md`
(`REQ-result-stale`). The earlier cutoff recorded
`oracleExecutionPolicy: gomutant/full-oracle@1`; that historical policy marker
is not a claim of current eligibility. Judge body and shaped records under the
live supported-policy and freshness contracts, preserving history without
promoting missing authority. The active plan records the initial complete-oracle
cutoff and subsequent admission repairs as completed prerequisites; those
repairs do not complete any campaign below. An installed tool enforcing the
current contracts and sound remeasurement are still required for final evidence.
Any future optimized narrowing needs independent execution authority.

## Outstanding campaign evidence

- Performance-evidence chunk 11's A/B execution and historical
  profile-comparison delta at `a41d8ee` requires campaign measurement under
  repaired oracle admission. This is retained campaign scope, not a recorded
  invocation or a campaign pass. Its passed named mutation probes do not
  discharge that obligation; include the entire delta in sound remeasurement
  at the authorized 9.3 execution, after scope reconciliation at 9.1.
- Full campaign `1e11674dc9cefba4`, budget 4: 44 targets, 171 selected
  candidates. It stopped without a summary at 75m30s, with 6 targets banked,
  24 generated candidates, 11 kills, 1 discard and 12 open survivors.
  No measurement claim is made over the remaining 38 targets; this campaign
  is incomplete, not passed.
- Targeted remeasurement `e8423761e551fb96`: 4 targets, 16 generated
  candidates, 15 kills and 1 survivor, `gc.go:41:5` force-false.
  This narrower selection does not complete the full campaign.
- All 12 named hand probes: 10 killed; the 2 admission duplicate-check
  probes were judged equivalent and attested by the main agent. These are
  specific probe dispositions, not campaign-wide coverage.
- A full-oracle audit made the deletion mutant at `gc.go:41` kill through
  a subprocess testcase. The force-false mutant remained a survivor under
  unsound negative child coverage. It is not established equivalent; its
  disposition requires sound remeasurement under the repaired oracle.

Earlier incomplete selections also require reconciliation at the final gate:

- The three-target newStatCmd/validateOptions/Compare campaign (run ID not
  retained here) measured 24 candidates and banked 3 machine-local records
  (22 killed, no open survivors), but
  reached its deadline during final evidence work. Cwd-sensitive observation
  refusals and bounded freshness-proof passes left subjects unproven.
- Native-fingerprint campaign `82f26be60d13cf04` selected 22 targets and
  1,064 candidates. Its 30-second freshness-proof budgets left subjects
  unproven; the 30-minute command deadline arrived after 3 attempts and
  before any target committed. It banked no kills or survivor judgments.
- A JSON-rendering campaign (run ID and exact symbol not retained here) banked
  its sole target with both mutants killed, then stopped reporting at roughly
  8 minutes despite a 10-minute command deadline; the enclosing 30-minute
  shell deadline terminated it without a finished summary. Shared
  deadline/close-out work is tracked in gomutant
  `docs/issues/campaign-silent-past-its-deadline.md`.
- Metric-registry campaign `a6d4f75aaf58cae4` used
  `gomutant run --changed HEAD --symbol github.com/greatliontech/pew/internal/metric.* --budget 4 --jobs 4 --timeout 30m --analysis-budget 30s`.
  It selected 5 metric functions with their full derived oracles. The
  freshness-proof pass left 332 subjects unproven; the command deadline
  arrived while still in baseline work, with no committed measurement.
  The non-registry delta was not selected. This bounded attempt establishes
  neither exhaustive coverage nor a passing campaign; the unmeasured
  remainder stays in the 9.1 inventory for sound remeasurement at 9.3.

## Profile-boundary scope

The profile-boundary selection `9eeabef27df54358` requested four candidates each
for `internal/profiles.Analyze` and `Decode`, retaining both derived oracle
packages. The 30-second proof budget left 224 subjects unproven. The baseline
completed and four of eight candidate attempts finished before the 30-minute
command deadline, but no target committed. There are no banked kills or survivor
judgments from that selection; the remaining profile delta was not selected.
Its named invariant-breaking probes remain separate evidence.

## Scope recovery and resolution

Recover the missing run IDs and exact symbol inventories from retained findings,
logs and repository history before defining final invocations. In particular:

- Recover the newStatCmd/validateOptions/Compare and JSON-rendering run IDs,
  the JSON symbol, and any unavailable invocation/selection details; their
  absence is a recovery obligation, not permission to invent a completed run.
- For `1e11674dc9cefba4`, retain all 44 targets, the 171 selected candidates,
  the measured prefix and the 38 unmeasured targets. The four targets of
  `e8423761e551fb96` and the twelve hand probes are separate scopes.
- Resolve the historical `HEAD` used by `a6d4f75aaf58cae4`, recover its five
  exact metric symbols, and inventory the non-registry delta it excluded.
  The command above is a historical invocation, not a command to run against
  today's `HEAD`.
- Recover the exact 22-target inventory of `82f26be60d13cf04` and the
  `a41d8ee` delta's complete scope. Preserve the profile delta excluded by
  `9eeabef27df54358` in addition to its two named functions and two oracle
  packages.

Map renamed or removed historical subjects explicitly to the retained obligation
and its current witness. A fresh `--changed HEAD` cannot substitute for any
missing historical scope. At 9.3, measure only the named authorized scopes with
sound full-oracle attribution, disposition survivors, and report actual
completion and freshness-proof states separately. Unsupported observations
remain explicitly unsupported. Focused kills cannot stand in for unmeasured
targets, unproven freshness or a missing final summary.
