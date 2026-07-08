# Runtime-config environment (GOGC, GODEBUG, GOMEMLIMIT, GOMAXPROCS) is outside every guard

Lands: when the guard model is extended via the spec-amend channel, or folded into the tracked
`runconditions` upgrade path (spec §9) if that lands first

## Fault

Environment variables read by the Go runtime itself are invisible to all five guards:

- Not in `buildconfig`: `internal/provenance/provenance.go` (`buildConfig`) digests only
  build-affecting keys (GOAMD64/GOARM/…, CGO_ENABLED, GOFLAGS, GOEXPERIMENT).
- Not in the runtime-input manifest: the runtime reads GOGC/GODEBUG/GOMEMLIMIT/GOMAXPROCS during
  runtime init, *before* the testlog stream starts, so no `getenv` line is ever emitted (spec §7.8
  documents the pre-testlog blind window for file I/O; the same window hides these).
- Deliberately not in the machine fingerprint (§8 excludes transient conditions).

Concrete failure: record with default `GOGC`; later export `GOGC=off` in the shell. Every
allocation-heavy recording still reads `valid`, but a rerun under the current environment would
produce materially different numbers — the stored result no longer describes what a fresh run would
measure, with no guard moving. `GOMAXPROCS` additionally changes the `-N` result-name suffix, so
after a change the overwritten file's rows stop aligning with baselines (`pew stat` emits
"only present in base" notes rather than an actionable mismatch).

## Reconciliation

These are process-env facts capturable for free at run time (unlike the governor, which motivated
excluding run conditions from identity in §8). Candidate shapes:

- Fold the runtime-config keys into the `buildconfig` digest (cheapest; slightly stretches the
  name), or
- Add a distinct `runtimeconfig` provenance line + guard (cleaner naming; one more guard in §7), or
- Record them under the §9 `runconditions` line with a comparison-mismatch warning instead of a
  staleness guard (weakest: warns at `stat` time rather than invalidating).

Guard-model extensions are spec-tier; whichever lands amends §5/§7 and adds anchor tests (change
GOGC ⇒ recording non-valid, or ⇒ stat warning, per the chosen shape).
