# Historical Stat Inventory

Lands: when changing `pew stat` inventory or git-ref recording enumeration

## Fault

`pew stat` enumerates benchmarks from the current worktree and then reads recordings for those names
from the selected refs. In A/B or pinned modes, this can omit recordings for benchmarks that exist in
the historical refs but no longer have a current source declaration.

Concrete failure path:

- `BenchmarkOld` exists and has recordings in both `refA` and `refB`.
- The working tree deletes or renames `BenchmarkOld`.
- `pew stat refA refB` scans current benchmark declarations and never asks git for
  `BenchmarkOld.txt`.
- The comparison silently omits a benchmark that is present on both requested sides.

## Reconciliation

For historical comparisons, enumerate the union of stored recording paths from the selected refs and
the working-tree store, then read those recordings directly. Current source declarations may still be
used for working-tree staleness warnings, but they must not be the inventory authority for historical
recordings.
