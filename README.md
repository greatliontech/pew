# pew

`pew` manages Go benchmark recordings with provenance and staleness checks.

## Workflow

1. Run benchmarks and store recordings:

   ```sh
   pew run ./...
   ```

2. Check whether stored results still match the current tree:

   ```sh
   pew status ./...
   pew status --stale ./...
   ```

3. Re-run only recordings that are not currently valid:

   ```sh
   pew run --stale ./...
   ```

4. Compare recorded results:

   ```sh
   pew stat
   pew stat HEAD~1
   pew stat main HEAD
   ```

   In CI, gate on regressions with `pew stat --fail-on-regression`. The gate fails
   closed: a regression on a gated metric exits `1`, and a comparison that measured
   nothing on a gated unit — no recordings yet, or every candidate skipped — exits `2`
   with a diagnostic naming why, instead of passing vacuously. When some benchmarks
   compare and others are skipped, the compared subset alone decides the exit. Without
   the flag, `pew stat` is informational and exits `0`.

5. Remove recordings for benchmarks that no longer exist:

   ```sh
   pew gc
   ```

## Verdicts

- `valid`: all guards match and the recording can be reused.
- `stale`: code, runtime inputs, toolchain, machine, or build config changed.
- `unverifiable`: pew cannot prove the recording is reusable, so rerun it.
- `unrecorded`: no stored result exists yet.

Go's testlog omits operation outcomes, so testlog identities alone cannot prove runtime-input
observation completeness. pew completes each benchmark invocation's observation with a pre-spawn
bracket and a completed-process ingest, so a benchmark whose closure reaches no unverifiable
runtime input verifies, while a file-reading benchmark is refused on its own closure reason; an
observation that cannot complete records explicit incomplete evidence with its reason. An
explicit `--assume-pure` or `//gofresh:pure` assertion is the documented full-trust override. Current recordings carry `pew-format: 2`; unversioned or unknown formats are
rejected and must be regenerated.

By default recordings live under `<module>/benchmarks`. Use `--bench-dir` on the
commands when a different storage directory is needed.

## Benchmark Defaults

`pew run` measures each selected benchmark in its own `go test` process (single-subject
execution): sibling benchmarks never share process state, each recording carries the runtime
evidence of exactly its own invocation, and a failing benchmark discards only its own recording
while its siblings record. Per benchmark it records output from:

```sh
go test -run '^$' -bench '^BenchmarkName$' -benchmem -count 10 -benchtime 1s <pkg>
```

- `-run '^$'` skips tests; pew records benchmarks only.
- All benchmarks are selected by default; use `--bench` to narrow the selection (each selected
  benchmark still measures in its own process).
- `-benchmem` is always enabled so memory metrics are stored with timing metrics.
- `--count 10` records enough samples for meaningful comparison.
- `--benchtime 1s` keeps each sample time-based and compatible with Go's auto-scaling benchmark loop.

Use `--pin` for CPU affinity and `--strict` to make run-hygiene warnings fatal. The observed run
conditions (governor, turbo/boost, 1-minute load, thermal throttling, battery) are recorded with
every result as the `pew-runconditions` line — unobservable signals are recorded as explicit
`unknown` — so a stored baseline documents the conditions it was measured under. Run conditions are
provenance only: they never make a recording stale, and `pew stat` prints a note (while still
comparing) when the two sides were recorded under different conditions. Build-affecting Go
flags such as tags, gcflags, ldflags, PGO, and cgo/compiler inputs are deliberately not generic
pass-through flags; they must be covered by pew's `buildconfig` guard before being exposed.
