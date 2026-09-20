# audit-note-renderers-one-grammar

`internal/compare` renders an audit row's difference two ways: `conditionsNote`
for `pew-runconditions` (categorical-field equality, "unrecorded on base side",
"mixed run conditions within the base side") and `auditNotes` for every other
audit row (string equality, "(none)" for an absent side, "mixed X within both
sides"). The audit SET is the registry's (spec §5's `audit?` column) and the
run-conditions row is excluded from the generic fold by name at its one site;
the two renderers differ in grammar, in the comparison, and in the empty
encoding, so a reader learns two vocabularies for one mark.

Collapse: one renderer over every audit row with a per-row comparison (the
categorical predicate for run conditions, equality elsewhere) and one grammar
for the absent and mixed arms — a served-text change on the run-conditions
notes, pinned by the compare goldens.

Lands: cross-tool train chunk 252 (the hygiene sweep).
