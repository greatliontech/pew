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

Reconcile the duplicate purpose prose under the spec's authority:
§12 and REQ-pew-verb-defaults require the purpose on the first row naming
an opt-in; served guidance must conform to that contract. Any collapse
to a reference or generated projection must preserve that requirement
or first settle a spec amendment. The guidance document is not authority
to remove or weaken the spec's purpose requirement.

Invariants preserved: the table stays the spec's behaviour and defaults
contract; the served guidance stays the document's.

Lands: performance-evidence plan chunk 12.2 (the guidance reconcile; audit 335 — the train's 232 had delegated it there while this doc still named 232).
