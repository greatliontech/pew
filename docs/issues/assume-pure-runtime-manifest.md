# `--assume-pure` cannot override manifest-level unverifiability

Lands: when the purity-override semantics are decided via the spec-amend channel (code-vs-spec
divergence — user decides direction; spec wins by default)

## Fault

`internal/stale/stale.go` (`Check`): `runtime.Unverifiable` returns `Unverifiable` *before* the
`pure` config switch is consulted. So `pure: true` suppresses only closure-level Class-B
(`head.Unverifiable`), never unverifiability recorded in the runtime-input manifest.

Concrete failure: a benchmark `stat`s a fixed fixture file. The testlog `stat` op produces a
"stat metadata input" entry in the manifest's `Unverifiable` list
(`internal/runtimeinputs/runtimeinputs.go`, `FromTestLog`), which makes `runtime.Unverifiable` true
on **every** subsequent check. Rerunning with `--assume-pure` records `pure: true`, but the verdict
stays `unverifiable` forever — the benchmark can never converge to `valid`, defeating the reuse win
for exactly the case spec §7.5 names ("a fixed fixture file").

Spec conflict: §7's `unverifiable` verdict says "the user can assert purity to override (§7.5)",
with no carve-out for manifest-recorded reasons. The code enforces a narrower rule than the spec
states.

## Reconciliation

Two candidate directions, user's call:

- **Code follows spec:** `pure: true` also suppresses `runtime.Unverifiable` (the author takes
  responsibility for all observed-input unverifiability, including stat-metadata and
  unrecognized-op entries). The hashable parts of the manifest still guard normally.
- **Spec narrows:** §7/§7.5 are amended to state that purity assertion overrides only closure-level
  Class-B detection, and manifest-level unverifiability always forces a rerun. Then document that a
  stat-observed fixture has no reuse path.

Whichever lands, add a regression test pinning the chosen behavior for a `pure: true` recording
whose manifest carries an `Unverifiable` entry.
