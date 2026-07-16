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

5. Remove recordings for benchmarks that no longer exist:

   ```sh
   pew gc
   ```

## Verdicts

- `valid`: all guards match and the recording can be reused.
- `stale`: code, runtime inputs, toolchain, machine, or build config changed.
- `unverifiable`: pew cannot prove the recording is reusable, so rerun it.
- `unrecorded`: no stored result exists yet.

Go's testlog omits operation outcomes, so a successful benchmark process cannot prove
runtime-input observation completeness. New recordings therefore carry explicit incomplete
runtime evidence and remain `unverifiable` even when the benchmark performs no I/O or ignores a
transient read error. An explicit `--assume-pure` or `//gofresh:pure` assertion is the documented
full-trust override.

By default recordings live under `<module>/benchmarks`. Use `--bench-dir` on the
commands when a different storage directory is needed.

## Benchmark Defaults

`pew run` records benchmark output from:

```sh
go test -run '^$' -bench . -benchmem -count 10 -benchtime 1s <pkg>
```

- `-run '^$'` skips tests; pew records benchmarks only.
- `-bench .` runs all benchmarks by default; use `--bench` to select a subset.
- `-benchmem` is always enabled so memory metrics are stored with timing metrics.
- `--count 10` records enough samples for meaningful comparison.
- `--benchtime 1s` keeps each sample time-based and compatible with Go's auto-scaling benchmark loop.

Use `--pin` for CPU affinity and `--strict` to make run-hygiene warnings fatal. Build-affecting Go
flags such as tags, gcflags, ldflags, PGO, and cgo/compiler inputs are deliberately not generic
pass-through flags; they must be covered by pew's `buildconfig` guard before being exposed.
