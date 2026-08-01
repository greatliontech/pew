# Bench recordings are born unverifiable: pew never wires the observation harness

Lands: first pew work item after gofresh's memo chunks (9-10) land —
before the gomutant train.

## Observed (two field reports, sharpened)

Every format-2 recording carries
`pew-runtime-inputs: {"unverifiable":["testlog lacks operation outcome
evidence"]}` and recording verification can never pass: the
verification layer is decorative for benchmarks. Comparisons still work
(provenance inputs match), so gates are not blocked — but the runtime
guard earns nothing.

## Root cause

pew's run path (cmd/pew/run.go) unconditionally constructs an
incomplete observation with that fixed reason. It never passes
-test.testlogfile to the bench invocation and never captures a
pre-spawn observation bracket. The "structural" impossibility is a
leftover assumption from before gofresh's bracket machinery existed:
stipulator and gomutant produce completed, verifiable observations for
test processes today via testlog capture plus a pre-spawn bracket over
the package directory and declared bracket paths — nothing in the bench
harness precludes the identical wiring.

## Direction

Adopt the sibling tools' pattern: testlog capture on the bench
invocation, pre-spawn bracket (module-relative package dir + declared
bracket paths), FromTestLogEnv with the completed-process and bracket
options, manifest attached to the recording (§7.8). The incomplete
construction remains only as the fallback for a process that dies
before harness completion. Until the wiring lands, the header keeps the
honest incomplete manifest (it is load-bearing: absence would assert
no runtime inputs and serve — REQ-inputs-absent-asserted).
