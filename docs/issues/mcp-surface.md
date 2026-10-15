# Whether pew owes an MCP surface

Lands: Gofresh docs/plans/tooling-redesign.md 8.3; a new MCP transport requires a concrete unmet driving-agent workflow.

Pew serves its CLI with JSON on `status`, `stat` and `ab`, including the
implemented profile reports. Long operations already have a thirty-second
progress channel on the operator's log, separate from machine-readable
stdout (`REQ-pew-progress`). Comparison text/JSON parity and this progress
channel are not missing MCP prerequisites to rebuild.

Structured output for `run` and `gc` remains a reader-workflow question.
Name the consumer, the facts it cannot reliably obtain today, and the required
completion/interruption account before adding a form. Long-call recovery,
bounded output and progress delivery must follow the actual workflow rather
than a presumption that every measurement belongs in one synchronous call.

A new MCP transport requires a concrete driving workflow unmet by the existing
CLI/JSON/progress surfaces. The existence of an LLM reader or sibling tools'
MCP servers alone does not satisfy that condition. If the condition holds,
derive the verb set, project context, envelopes, defaults and recovery from
that reader's needs under the canonical contracts.

Invariants preserved either way: the CLI's verb-defaults table stays
the CLI's contract; a second surface derives its own defaults and is
never the CLI rendered through a wrapper.
