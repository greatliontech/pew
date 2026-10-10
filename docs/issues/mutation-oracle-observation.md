# Incomplete mutation campaigns and subprocess coverage

Lands: performance-evidence plan chunk 12 final mutation gate, after the shared
gomutant subprocess-coverage design fix and remeasurement with sound oracle selection.

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
(`REQ-result-stale`): complete-oracle measurements carry
`oracleExecutionPolicy: gomutant/full-oracle@1`, and legacy body records without
supported policy require whole remeasurement rather than retaining scores
through a serve or partial extension. The final gate needs an installed tool
enforcing those contracts. Historical campaign results below remain incomplete
or affected until sound remeasurement; a shared-tool fix is not itself that
measurement. Any future optimized narrowing needs independent execution authority.

## Outstanding campaign evidence

- Chunk 11's A/B execution and historical profile-comparison delta also
  requires campaign measurement after the confirmed negative-child-coverage
  fault above is fixed. This is blocked campaign scope, not a recorded
  invocation or a campaign pass. Its passed named mutation probes do not
  discharge that obligation; include the entire delta in sound remeasurement
  before the chunk 12 final mutation gate.
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
  disposition requires the shared coverage fix and sound remeasurement.

Earlier incomplete selections also require reconciliation at the final gate:

- The newStatCmd/validateOptions/Compare campaign measured 24 candidates
  and banked 3 machine-local records (22 killed, no open survivors), but
  reached its deadline during final evidence work. Cwd-sensitive observation
  refusals and bounded freshness-proof passes left subjects unproven.
- Native-fingerprint campaign `82f26be60d13cf04` selected 22 targets and
  1,064 candidates. Its 30-second freshness-proof budgets left subjects
  unproven; the 30-minute command deadline arrived after 3 attempts and
  before any target committed. It banked no kills or survivor judgments.
- A JSON-rendering campaign banked its sole target with both mutants killed,
  then stopped reporting at roughly 8 minutes despite a 10-minute command
  deadline; the enclosing 30-minute shell deadline terminated it. Shared
  deadline/close-out work is tracked in gomutant
  `docs/issues/campaign-silent-past-its-deadline.md`.
- Metric-registry campaign `a6d4f75aaf58cae4` used
  `gomutant run --changed HEAD --symbol github.com/greatliontech/pew/internal/metric.* --budget 4 --jobs 4 --timeout 30m --analysis-budget 30s`.
  It selected 5 metric functions with their full derived oracles. The
  freshness-proof pass left 332 subjects unproven; the command deadline
  arrived while still in baseline work, with no committed measurement.
  The non-registry delta was not selected. This bounded attempt establishes
  neither exhaustive coverage nor a passing campaign; the unmeasured
  remainder stays at chunk 12 with the existing shared-coverage repair
  and sound remeasurement requirement.

## Required resolution

The profile-boundary selection `9eeabef27df54358` requested four candidates each
for `internal/profiles.Analyze` and `Decode`, retaining both derived oracle
packages. The 30-second proof budget left 224 subjects unproven. The baseline
completed and four of eight candidate attempts finished before the 30-minute
command deadline, but no target committed. There are no banked kills or survivor
judgments from that selection; the remaining profile delta was not selected.
Its named invariant-breaking probes remain separate evidence.

Fix shared oracle-coverage faults in gomutant, preserving the mandatory
integration testcase. Reconcile and remeasure all incomplete selections and
the blocked chunk 11 scope with sound full-oracle attribution;
disposition every remaining survivor and report
the campaign's actual completion and freshness-proof state. Unsupported
observations remain explicitly unsupported. Focused kills cannot stand in for
unmeasured targets or a missing final summary.
