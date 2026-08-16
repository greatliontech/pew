# Profile capture and attribution as recording companions

**Composition note carried from the closed derivation-ab-mode doc:
per-side profile capture (`-cpuprofile`/`-benchmem` per A/B side)
belongs in `pew ab` itself - profile attribution is part of the same
derivation loop, and the mode's standing binaries make per-side
capture free.

Lands:** cross-tool train chunk 15 (gofresh
docs/plans/cross-tool-train.md), after the one-go-invocation chunk —
folded from the original condition: profile evidence needing machine
checking, with tugboat's hand-run bench protocol the standing consumer.

pew owns curves, provenance, and comparison; the *attribution* half of
a measurement is a hand protocol in consumers. tugboat's standing
discipline requires one `-cpuprofile` run per new or re-shaped arm
whose top functions must show the claimed subject before the recording
lands — conclusions recorded as prose in the commit record, the raw
profile discarded as derivation evidence. Two capability gaps follow:

- **Attribution is unverifiable after the fact.** The "subject
  confirmed" verdict is prose; nothing pins which functions dominated
  at recording time, so a curve whose hot path later drifts (inlining
  change, a quiesce defect turning an active arm into a skip-branch
  measurement) keeps its stale attribution silently. The consumer's
  rule exists because exactly that happened once, and it was caught by
  review, not by tooling.
- **Curve shifts are unexplainable.** `pew stat` can flag a
  significant sec/op regression between two recordings whose closure,
  toolchain, and machine hashes all match — and with no stored
  profiles there is nothing to diff: "environmental" is asserted,
  never shown.

Sketch: `pew run --profile` captures a cpu profile per arm (and a mem
profile where the recording carries B/op claims) and stores top-N
attribution — or the full profile — beside the recording under the
same provenance conjunction; `pew status` gains an attribution verdict
(do the recorded subject symbols still dominate?); `pew stat` gains a
profile-diff view for flagged regressions. The consumer's hand
protocol stays valid as the derivation loop; the stored attribution
becomes the artifact of record.
