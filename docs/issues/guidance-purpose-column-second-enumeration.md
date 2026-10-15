# §12's purpose column restates the knob prose

Lands: Gofresh docs/plans/tooling-redesign.md 8.3.

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

The old performance-evidence 12.2 and charter 232 entries are the residual
inventory for this work. The active slot is 8.3; a dependency bump or the
already-adopted shared Coverage lint does not reconcile this separate purpose
enumeration.
