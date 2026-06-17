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

Runtime inputs observed through Go's testlog are stored as input identities plus
a digest. Environment values are hashed but not stored in clear text.

By default recordings live under `<module>/benchmarks`. Use `--bench-dir` on the
commands when a different storage directory is needed.
