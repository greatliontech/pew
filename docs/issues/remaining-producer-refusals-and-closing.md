# Remaining producer refusal and closing boundaries

Lands: Gofresh docs/plans/tooling-redesign.md 5.1u, before opening the
Stipulator consumer migration. Each component has its own regression and
change-set boundary.

The following paths are source-traced, not newly executed reproductions.

## Consumer-owned environment queries

`internal/gotool.Reader` uses `Runner(prepare)` without containment. The
underlying collected command can wait on a descendant's inherited output pipe
after cancellation kills the direct launcher. Ordinary arm and diagnostic
closing reach these readers through their guard/provenance queries; an owned
closing deadline consequently need not bound their return. This is Pew's own
query path, separate from the package loader's declared containment exception.

Exercise a go launcher whose environment-query child retains a pipe, gated to
the specific query under test. Preparation cancellation and post-success closing
expiry must return within the stated containment bound, sweep descendants, name
the cause and retain earlier durable units. Preserve the distinct complete
environment-document salvage and listing-refusal rules.

## Asymmetric live A/B diagnostic obligations

`captureABProfiles` takes the union of both prepared inventories. A benchmark
absent from one side produces `no completed measurement`, which is appended to
the operational failures. With valid asymmetric populations, this makes the
command exit 1 instead of reporting unavailable/unfulfilled evidence and exit 2
under REQ-pew-profile-comparison-report. No capture failed on the absent side.

Use a shared benchmark and a benchmark declared on only one side, with actual
nonempty requested diagnostics. Both text and JSON retain the unavailable side
and independently completed capture. Pin unfulfilled, genuine capture/write
failure and interruption precedence; keep ordinary statistical rows intact.

## Explicit timing effort

The A/B preparation validates count, pattern and profile budget, but does not
validate nonempty ordinary `benchtime`. A known-invalid value reaches package
listing, reference materialization and both builds before the test binary
refuses it. Reuse the existing budget grammar at the entry boundary, before
those stages. Preserve omission's Go-default meaning and valid duration/count
forms. Test both the command entry and the operation helper so one path cannot
bypass the refusal.
