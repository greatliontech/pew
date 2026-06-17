# Runtime File-I/O Observability

Lands: when changing runtime file-I/O coverage or the `stale.Check` file-I/O bypass

## Fault

The runtime-input guard relies on Go's testlog stream to prove observed file inputs. Some file names
or operations may not be emitted by the testlog writer. If a benchmark opens a path that is not logged,
the manifest can omit the input, `pew-runtime` can remain unchanged, and the closure's file-I/O
unverifiable marker can be suppressed incorrectly.

Concrete false-valid path:

- Benchmark reads a mutable file whose path is not emitted by testlog, such as a path containing a
  newline if the Go testlog writer drops that name.
- The recorded manifest contains no path entry for that file.
- The file changes later.
- Source, toolchain, machine, and buildconfig guards match; runtime digest also matches because the
  input was omitted; status can report `valid`.

## Reconciliation

Specify and enforce the observability boundary. Options include:

- prove from the Go version's testlog implementation that every file-I/O path shape pew suppresses is
  logged, and keep unloggable path shapes `unverifiable`
- keep file-I/O closures `unverifiable` unless a stronger mechanism proves complete observed path
  coverage
- add a run-time sentinel or wrapper mechanism that detects dropped/invalid testlog names

The implementation must not turn a closure-level file-I/O `unverifiable` into `valid` unless the
manifest proves complete coverage for that benchmark's file observations.
