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
counters. When other test batches reach the target, narrowing can incorrectly
use the missing child counters as negative coverage and exempt a mandatory
subprocess testcase. The shared coverage model needs its own design fix before
this selection can support a final mutation judgment. The read-only diagnosis
is session `ses_ee11e73c3ffe1Rw2GG7WM1jzno`; the owning-tool issue is being filed
by the main agent for the performance-evidence chunk 12 final mutation gate.

## Outstanding campaign evidence

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

## Required resolution

Fix shared oracle-coverage faults in gomutant, preserving the mandatory
integration testcase. Reconcile and remeasure the incomplete selections with
sound full-oracle attribution; disposition every remaining survivor and report
the campaign's actual completion and freshness-proof state. Unsupported
observations remain explicitly unsupported. Focused kills cannot stand in for
unmeasured targets or a missing final summary.
