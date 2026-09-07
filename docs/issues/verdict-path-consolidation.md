# Verdict-path consolidation: one vouch resolver, one row decode

Two structural-collapse candidates surfaced while the admissibility
ladder and the store-owned vouch file landed. Neither is a defect;
each is two mechanisms where one would do.

## Vouch source: four process-wide globals → one resolver value

The engine's dynamic-state vouch set is assembled from four globals
in `cmd/pew`: `dynamicStateVouches` (the parsed `--vouch` flags, set
by `configureDynamicStateVouches`), `vouchStoreDir` (set by each
judged verb's RunE from `--bench-dir`), `storeVouchMemo` (the file
read once per store), and `engineVouches(moduleDir)` joining them.
The state is process-wide because the engine builders
(`buildEngine`, `newEngineAtProducer`) take no verb context.

Collapse: a `vouchSource{flags []string; storeDir string}` value
resolved once per invocation at the verb's preparation record and
threaded to the engine builders, so a test never has to reset four
globals and a verb cannot build an engine before its store is
named (today `TestJudgedVerbsNameTheirStoreForTheVouchFile` pins
the ordering per verb). Invariants preserved: file ∪ flags, flags
never remove, one file per store (REQ-pew-vouch-source).

## Admission: one recording decoded three times per row

`admitRecording` walks each row through `store.IsRecordingShape`
(one config map), `fingerprintFromConfig` (a second), and
`closedSetValues` (a third). One decode per row into a
`recordingRow{fp, ledger, closed map[string]string}` would feed all
three rungs and the explain path (`checkPackage` re-derives the
fingerprint for `--explain`).

Invariants preserved: every row judged; closed-key agreement across
rows; strategy rung on the working-tree side only (REQ-pew-admission).

Lands: cross-tool train chunk 174