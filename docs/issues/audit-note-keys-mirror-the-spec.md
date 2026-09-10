# compare's audit-note keys and §5's audit rows are two hand-kept lists

`internal/compare.auditNoteKeys` names the recording lines whose
difference between two sides is a note, never a grouping key; §5's key
table says which rows are audit. The two agree by hand today (five
lines), and TestRecordingConfigKeysMirrorSpec binds only the full key
set, not the audit subset — a row marked audit in §5 without its note
in compare is silent. One mirror pin over the table's audit rows, or
the registry carrying the audit mark the note reads, closes the gap.

Lands: cross-tool train chunk 251 (one recording-key registry with per-key marks, 237 P1).
