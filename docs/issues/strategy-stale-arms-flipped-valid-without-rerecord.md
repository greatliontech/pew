# 46 tugboat arms flipped `stale (dynamic-state strategy)` → `valid` between sweeps with no re-record: one of the two verdicts is wrong

Field report from tugboat (filed uncommitted in this tree, the
standing channel), drawn from the weekly fleet sweep's judge logs and
tugboat's `docs/issues/pew-store-stale-fleet-sweep.md` (the doc that
tracked the composition week by week).

- 2026-09-07 sweep: tugboat's store reports 56 non-valid arms, every
  one `stale (dynamic-state strategy)` — "a recorded dynamic-state
  strategy the installed engine no longer matches", judged before the
  closure comparison.
- 2026-09-14 sweep: rows byte-identical to 09-07 (the judge log's
  first line: `pew-tugboat 56` identical).
- 2026-09-21 sweep: 10 non-valid arms, all `stale (format)`; the
  other 46 of the 56 read `valid`. No tugboat commit landed between
  the sweeps (the reflog's last entry is the 09-07 filing) and nothing
  was re-recorded; the only thing that moved was the installed pew —
  rebuilt at HEAD both times, its gofresh pin v0.101.3 → v0.102.0
  (pew c05a5f7, the build! bump).
- 2026-09-28 sweep: identical to 09-21.

So the same 46 recordings were served stale under one engine build
and valid under the next, with the store untouched. Exactly one of
those verdicts is right, and the ladder has no record saying which:
either 46 recordings were refused for a strategy mismatch that did
not exist (a false stale — cost: a needless whole-store re-record
was scheduled), or 46 recordings whose recorded strategy the engine
does not match are being served as valid now (a false valid — the
worse direction: `pew stat` verdicts against them are void without
saying so). The tugboat-side doc says "which verdict the ladder had
wrong is a pew-side question this doc does not answer"; this is that
question, filed where it can be answered.

What would settle it: the two engines' strategy keys for one of the
46 arms (e.g. `lifecycle` rebind or `internal/raft` RawNode — the
report capped the row list at 15; the store's `pew status --explain`
under both builds names the recorded and the derived strategy).
tugboat's resumption runs its whole-store `pew status --explain` this
week under the current build and will record the current key beside
the recordings' — the other half is the v0.101.3-pinned build's
derivation, recoverable from pew's history.

Lands: performance-evidence plan chunk 8 (the shared verdict path and explain row per validity key).
