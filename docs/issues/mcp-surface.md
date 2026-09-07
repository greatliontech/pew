# Whether pew owes an MCP surface

pew serves one surface, the CLI, with `--json` on `status` and `stat`
as its machine-facing output (spec §12). The other train tools
(gomutant, stipulator) serve an LLM through an MCP surface designed
for that reader: token-conscious envelopes, a self-starting
instruction naming the entry call and the loop, progress
notifications on long calls.

An LLM driving measurements today goes through the CLI: `run`, `ab`,
and `gc` print only the human rendering (no `--json`), long
measurements exceed a harness's call timeouts with
no progress channel, and the verb-defaults table (§12) derives every
default for a human at a terminal. Whether that reader is owed its
own surface — and if so, which verbs (a measurement verb that runs for
minutes is a poor MCP call; `status`/`stat`/`gc` fit), which envelope
caps, and whether `run`/`ab`/`gc` gain `--json` on the CLI as the
lesser step — is a product-scope judgment: it commits pew to a second
reader with its own contract and maintenance.

Invariants preserved either way: the CLI's verb-defaults table stays
the CLI's contract; a second surface derives its own defaults and is
never the CLI rendered through a wrapper.

Lands: cross-tool train chunk 177 for the JSON and progress step; the MCP surface is deferred 2026-09-07 (user decision: revisit when a driving agent appears)