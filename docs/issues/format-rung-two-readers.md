# format-rung-two-readers

The format rung — one `pew-format` line, byte-exact, no reader-side
`pew-format-invalid` annotation, the current version — is judged twice:
`store.IsRecording` over parsed results and `cmd/pew.fingerprintFromConfig`
over a recording's config lines, each building its own key map and re-checking
the count, the annotation, and the value. The two agree by construction today
(both read the registry's spellings) but are two implementations of one rule.

Collapse: one format judgment in the store (a config-slice form beside the
results form, or the fingerprint reader taking parsed results) so the rung has
one home; `fingerprintFromConfig` keeps only the fingerprint restoration.

Lands: cross-tool train chunk 275 (pew's bump behind gofresh 274, whose published fingerprint
wire form collapses the reader — moved from 252 at audit 264 so the work is done once).
