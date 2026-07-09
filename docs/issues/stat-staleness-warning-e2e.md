# `pew stat` staleness-warning path has no end-to-end test

Lands: when `pew stat`'s working-tree staleness check is next changed.

## Context

The auto/pinned-mode staleness warning (spec §10: a working-tree recording that is
non-valid for HEAD warns, never blocks) is exercised only compositionally: the
engine construction is pinned by `TestNewEngineHonorsDirectives`, the verdict
mapping by `TestApplyPurity`/`TestCheckOneAppliesMeasurementGuards`, and the
inventory/comparison plumbing by the `runStat` tests — but no test drives `runStat`
end to end into `engine.Check` on a real recording. A mutation swapping stat's
`newEngine(pkgs)` back to a bare `gofresh.New()` (dropping directive purity from the
warning, the exact defect fixed in this change set) survives the suite.

## What an e2e test needs

A temp git repo containing a loadable module with a benchmark (one variant carrying
`//gofresh:pure` over file I/O), recordings committed at a ref for the base side and
present in the working-tree store for the new side, and an assertion over the
warning stream — the existing `runStat` A/B-mode test harness covers the inventory
half but not a loadable module with real closures.

## Resolution

On landing: extend the `runStat` test harness with a loadable temp module, assert
the warning appears for a stale recording and stays silent for a directive-pure one,
then delete this doc — git holds history.
