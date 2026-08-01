# Bench headers flag runtime inputs "unverifiable" for a structurally evidence-free run class

Lands: when pew work resumes after gofresh's analysis-simplification
plan closes.

## Observed (field report)

Every v2 recording header carries
`pew-runtime-inputs: {"unverifiable":["testlog lacks operation outcome
evidence"]}` — pew's own run path constructs an incomplete observation
with that reason for every bench run (cmd/pew/run.go), because the Go
benchmark testlog structurally lacks operation outcome evidence. A
per-run "unverifiable" flag for a whole run class carries no
information and reads as a defect to consumers.

## Constraint on the fix

The incomplete manifest is load-bearing: its unverifiability is what
keeps bench results from serving on runtime-input evidence, and an
ABSENT manifest means the opposite ("caller asserts no runtime inputs"
— REQ-inputs-absent-asserted — which would serve). The fix is
presentational: a distinct structural marker for the run class whose
check semantics are byte-for-byte today's unverifiable manifest, or
header wording that names the structural cause instead of implying a
per-run verification failure. Never a bare removal.
