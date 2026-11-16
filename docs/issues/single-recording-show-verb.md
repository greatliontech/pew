# Single-Recording Summary & Hotspot Inspection Verb (`pew show`)

- **Status:** Open
- **Governing contract:** [`docs/specs/spec.md`](../specs/spec.md) (`REQ-pew-admission`, `REQ-pew-finite-samples`, `REQ-pew-historical-profile-evidence`, `REQ-pew-verb-defaults`, `REQ-pew-guidance`), [`docs/guidance.md`](../guidance.md)
- **Lands:** awaits triage

## Problem

`pew`'s existing CLI verbs cover measurement (`run`), freshness inventory (`status`), two-side comparison (`stat` and `ab`), and orphan cleanup (`gc`), leaving no command to inspect the numerical results of a single stored recording:

- `pew run` prints `recorded <pkg>.<Benchmark>` without summarizing the recorded sample distribution.
- `pew status` reports freshness verdicts (`valid`, `stale`, `unverifiable`, `unrecorded`) and `--explain` guard/profile diagnostics, never the recorded timing, allocation, throughput, or custom `b.ReportMetric` values.
- `pew stat` and `pew ab` require two sides (`working-tree vs HEAD`, `working-tree vs ref`, or `refA vs refB`). When a benchmark is newly added (no baseline on `HEAD` or `ref` yet) or an operator wants to audit a single stored baseline's absolute latencies, `B/op`, `allocs/op`, custom units, sample count, spread, and top CPU/allocation hotspots together, no `pew` command serves that view.
- Reading raw `benchmarks/**/*.txt` files directly is awkward and error-prone because format-6 recordings embed multi-hundred-kilobyte base64 `pew-fingerprint`, `pew-test-variant-ledger` (`.2`, `.3`, …), and `pew-profiles` (`.2`, `.3`, …) header lines alongside raw unaggregated sample rows.

## Resolution Scope

1. Add a read-only inspection verb (for example `pew show [packages]`, with optional `--ref <rev>` to read from a git revision without checkout) supporting `--bench`, `--label`, `--bench-dir`, `--profile[=cpu|alloc|cpu,alloc]`, `--top`, and `--json`, registered in `docs/specs/spec.md` §12 (`REQ-pew-verb-defaults`) and `docs/guidance.md` (`REQ-pew-guidance`).
2. Enforce `REQ-pew-admission` and `REQ-pew-finite-samples` on read, and for `--ref` enforce `spec.md` §11 and `REQ-pew-historical-profile-evidence` (pure `go-git` reads from a single invocation-pinned ref for recordings, embedded `sources`, and `.profiles/sha256/` objects with no working-tree fallback and no `git` subprocesses).
3. Summarize each selected recording's sub-benchmarks and metrics (`sec/op`, `B/op`, `allocs/op`, `MB/s`, and custom `ReportMetric` units) using `pew`'s statistical summary machinery (sample count `n`, median, spread/confidence interval, and recorded `pew-runconditions` / commit / dirty provenance), plus top attributed profile hotspots when `--profile` is requested.
