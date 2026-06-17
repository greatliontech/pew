# Store Symlink Boundary

Lands: when changing store path creation, writes, reads, or removals

## Fault

Store path validation rejects `..` and unsafe labels, but writes still use ordinary filesystem calls
under `bench-dir`. If an existing directory component under `bench-dir` is a symlink to outside the
store, `os.CreateTemp`/`os.Rename` can write outside the intended recording tree.

Concrete failure path:

- Worktree contains `benchmarks/internal -> /tmp/outside`.
- `pew run` writes package `internal/foo`, benchmark `BenchmarkX`.
- The store path is lexically under `benchmarks/internal/foo/BenchmarkX.txt`, but the kernel follows
  the symlink and writes under `/tmp/outside/foo/BenchmarkX.txt`.

## Reconciliation

Make store writes path-confined under the real store root:

- reject symlink directory components between `Store.Root` and the target file, or
- resolve the real root and real target directory and require the target to remain under the real root
  before creating temp files and renaming.

Reads/removes should use the same boundary so a symlink cannot cause `gc` or future commands to act on
files outside `bench-dir`.
