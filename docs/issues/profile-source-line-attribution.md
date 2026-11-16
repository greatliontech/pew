# Hermetic Line-Level Source Hotspot Attribution from Stored Profile Snapshots

- **Status:** Open
- **Governing contract:** [`docs/specs/spec.md`](../specs/spec.md) (`REQ-pew-profile-attribution`, `REQ-pew-profile-report`), [`docs/guidance.md`](../guidance.md)
- **Lands:** awaits triage

## Problem

Under `REQ-pew-profile-capture`, `REQ-pew-profile-attribution`, and `REQ-pew-profile-report`, `pew` already captures base64-encoded snapshots of known build-selected mutable Go source files (`sources[].bytes`) in the companion profile index and validates exact filename, symbol, and line evidence (`internal/profiles/source.go`).

However:

1. `pew`'s text and JSON reporting surfaces currently aggregate sample weights strictly at function-symbol granularity (`symbol`, `flat`, `cumulative`, `exact-source <file>`). When a function contains multiple allocations, loops, or branches, pinpointing which statement inside that function owns the `flat` or `cumulative` weight still requires manually locating the content-addressed blob under `benchmarks/.profiles/sha256/` and invoking `go tool pprof -list`.
2. `cmd/pew/profiles.go` and `internal/profiles/source.go` currently admit `exact-source` only for non-generic top-level `*ast.FuncDecl` declarations in the benchmarked package's own directory (`filepath.Dir(file) == p.Dir`), leaving `b.Run` closures, generic functions, and sibling in-module packages `unresolved` even when their exact mutable source files are part of the build-selected span.

## Resolution Scope

1. Extend `internal/profiles` to attribute per-line `flat` and `cumulative` sample weights against the immutable `sources` bytes stored in the companion profile index, preserving `REQ-pew-profile-attribution` invariants:
   - Profile filenames remain untrusted data, never host filesystem read instructions.
   - Leaf `flat` weights partition total sample weight across exact-source lines and retained `unresolved` leaves so `flat` sums equal `m.Total`; `cumulative` weights overlap across stack frames (deduplicated per `(source, line)` within a single sample) and are labelled as overlapping.
   - Evaluate widening `sources` capture and `exact-source` matching within the validated build-selected mutable module span to cover `b.Run` closures (`FuncLit` enclosing spans), generic functions, and sibling in-module packages where exact filename/symbol/line evidence holds.
2. Expose hermetic line-level hotspot rendering in human text views (`pew show`, `pew status --explain`, `pew stat --profile`, `pew ab --profile`, e.g. via `--lines` / `--source <symbol-pattern>`) and as additive fields on `--json` surfaces (`status --json`, `stat --profile --json`, `ab --profile --json`, `show --json`, keeping `--explain` and `--json` mutually exclusive), including across historical git refs.
