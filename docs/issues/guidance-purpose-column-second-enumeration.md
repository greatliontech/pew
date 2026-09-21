# §12's purpose column restates the knob prose

Spec §12's verb defaults table carries an `opt-ins (purpose)` column
restating every opt-in knob's purpose in its own words, beside the
guidance document's knob blocks — `--worktree-dir`: "a same-filesystem
placement where the repository's parent is unwritable or on another
device" against the document's "its purpose is a repository parent
that is unwritable or on another filesystem". The flag names and
defaults are pinned to the flag set (REQ-pew-verb-defaults); the
purpose wording is pinned to nothing and can drift from the
document's. (The usage literals, the third enumeration the earlier
filing named, render from the document since chunk 275.)

The collapse: the column names the opt-in only, its purpose the
document's knob clause (the table's row a pointer), or the column is
generated from the document's first clause and pinned to it.

Invariants preserved: the table stays the spec's behaviour and defaults
contract; the served guidance stays the document's.

Lands: cross-tool train chunk 232 (pew's spec chunk).
