# pew

`pew` manages Go benchmark recordings with provenance and staleness checks.

Use `pew version` or `pew --version` to identify the installed executable before
a field report. Both work outside a project and show embedded version, revision,
modified state, compiler and Gofresh dependency facts without running benchmarks.

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

   Status reports independent results even if another recording or package fails.
   An incomplete report exits nonzero; stale, unrecorded and unverifiable results
   alone are normal successful status output.

3. Measure what is not proven (a valid recording serves; `--all` re-measures everything):

   ```sh
   pew run ./...
   ```

4. Compare recorded results:

   ```sh
   pew stat
   pew stat HEAD~1
   pew stat main HEAD
   ```

   For a complete enforcing comparison in CI, specify its evidence policies:

   ```sh
   pew stat --fail-on-regression --coverage complete --freshness require --conditions compatible
   ```

   An eligible regression on a gated metric exits `1`. An empty eligible set
   or unsatisfied complete coverage exits `2` when no higher-precedence
   interruption, operational failure or eligible regression applies. Interruption
   exits `130`; operational failures retain their error exits. The currently implemented omitted policies
   are partial coverage with freshness and conditions reported, so the bare
   `--fail-on-regression` flag does not by itself require complete fresh compatible
   evidence. Without enforcement, statistical comparisons are informational;
   operational failures still return errors.

5. Remove recordings for benchmarks that no longer exist:

   ```sh
   pew gc
   ```

## Verdicts

- `valid`: all guards match and the recording can be reused.
- `stale`: code, runtime inputs, toolchain, machine, or build config changed.
- `unverifiable`: pew cannot prove the recording is reusable, so rerun it.
- `unrecorded`: no stored result exists yet.

Go's testlog identifies inputs but does not establish operation outcomes. Pew
prepares producing obligations before execution and binds the actual artifact,
process, environment and terminal receipt through independent closing checks.
Where the execution model lacks supported observation evidence, it retains
identity-only evidence and its cause; source/runtime guards and explicit
assertions retain their own authority. An explicit `//gofresh:pure` directive
is the documented full-trust override under the freshness contract. Current
recordings carry `pew-format: 6`; unversioned or unknown formats are
rejected and must be regenerated. Recognizable Pew recordings with malformed or
oversized legacy metadata report `stale (format)` and can be replaced by `pew run`
without manual deletion. Failed or interrupted measurements leave their old files
untouched. Old-format measurements are not admitted as historical comparisons.

By default recordings live under `<module>/benchmarks`. Use `--bench-dir` on the
commands when a different storage directory is needed.

## Benchmark Defaults

`pew run` builds the package's test binary first and measures each selected
benchmark in a separate process of that retained artifact. Sibling benchmarks
do not share process state, each recording carries its own invocation's evidence,
and a failed benchmark does not discard independently completed siblings. The
ordinary runtime arguments have this form:

```sh
<retained-binary> -test.run '^$' -test.bench '^BenchmarkName$' -test.benchmem -test.count 10 -test.benchtime 1s
```

- `-test.run '^$'` skips tests; pew records benchmarks only.
- All benchmarks are selected by default; use `--bench` to narrow the selection (each selected
  benchmark still measures in its own process).
- `-benchmem` is always enabled so memory metrics are stored with timing metrics.
- `--count 10` records enough samples for meaningful comparison.
- `--benchtime 1s` keeps each sample time-based and compatible with Go's auto-scaling benchmark loop.

Use `--pin` to pin the measurement to one CPU set derived from the host's topology (reported with its derivation before the run; a pinned recording and an unpinned one stale each other, so keep both under distinct `--label`s) and `--strict` to make run-hygiene warnings fatal. The observed run
conditions (governor, turbo/boost, 1-minute load, thermal throttling, battery) are recorded with
every result as the `pew-runconditions` line — unobservable signals are recorded as explicit
`unknown` — so a stored baseline documents the conditions it was measured under. Run conditions are
provenance only: they never make a recording stale, and `pew stat` prints a note (while still
comparing) when the two sides were recorded under different conditions. Build-affecting Go
flags such as tags, gcflags, ldflags, PGO, and cgo/compiler inputs are deliberately not generic
pass-through flags; they must be covered by pew's `buildconfig` guard before being exposed.
