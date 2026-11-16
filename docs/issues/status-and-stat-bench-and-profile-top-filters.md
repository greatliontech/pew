# `--bench` Selection and Profile `--top` Filtering on `pew status`, `pew stat`, and `pew ab`

- **Status:** Open
- **Governing contract:** [`docs/specs/spec.md`](../specs/spec.md) (`REQ-pew-comparison-coverage`, `REQ-pew-profile-comparison-report`, `REQ-pew-regression-gate`), [`docs/guidance.md`](../guidance.md)
- **Lands:** awaits triage

## Problem

1. **Missing `--bench` selector on `status` and `stat` (and package selector on `stat`):**
   - `pew run` and `pew ab` accept `[packages]` plus `--bench <pattern>` (`go test -bench` hierarchical `/`-separated regular-expression syntax via `splitBenchmarkPattern` / `restrictBenchmarkPattern`).
   - `pew status [packages]` accepts package patterns with no `--bench` selector, and `pew stat [ref | refA refB]` takes positional git revisions while hardcoding `./...` (`statPackages`) with neither `--bench` nor a package filter.
   - Inspecting or comparing one benchmark (or sub-benchmark) inside a multi-benchmark package or module therefore requires evaluating and rendering every declared or recorded benchmark.

2. **Unbounded symbol dumps in `status --explain` and `--profile` comparisons:**
   - When companion profiles are present, `pew status --explain` renders every symbol in every metric table, including dozens of `flat=0` runtime, test-harness, and package-init frames per benchmark (`~260 KiB` / `5,000+` lines on a 25-benchmark package).
   - Similarly, `writeProfileComparisons` (`cmd/pew/profile_compare.go`, shared by `pew stat --profile` and `pew ab --profile`) renders every function row sorted alphabetically by `Symbol` (`internal/profiles/compare.go`) with no top-N hotspot/delta ranking or truncation, forcing operators and agents to pipe `--json` through external scripts (`jq` / Python).

## Resolution Scope

1. Add `--bench` (`go test -bench` regexp syntax) to `pew status` (matching top-level `Benchmark*` declarations inventoried in `spec.md` §4/§6, and optionally filtering sub-benchmark profile context) and `pew stat` (supporting hierarchical `/`-separated sub-benchmark patterns over result identities per `REQ-pew-comparison-coverage`, plus optional `--pkg` package-pattern narrowing on `stat` since positional arguments are git refs), ensuring `stat --fail-on-regression` still exits `2` per `REQ-pew-regression-gate` when selection leaves zero eligible gated comparisons.
2. Add bounded hotspot controls (for example `--top=N`, ranking human display rows by flat weight on single-side views and absolute flat/delta magnitude on `stat --profile` and `ab --profile`, and omitting `flat=0` ancestor-only frames in default human text views unless cumulative/full output is requested), while preserving complete conserved symbol tables in `--json` or under `--top=0`.
