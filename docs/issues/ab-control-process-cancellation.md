# A/B control subprocess cancellation and cleanup outcomes

Lands: performance-evidence plan chunk 11

The benchmark/build path uses a cancellable contained process, but runAB reaches
gitTopLevel, addWorktree, sweepStaleWorktrees and gitCommonDir whose native Git
commands use exec.Command without the invocation context. A blocked Git command
or hook during worktree preparation can outlive cancellation before any measured
pair. Cleanup also invokes Git without a bound and currently cannot return its
failure to the operation result.

Complete the A/B control-operation contract alongside its per-side evidence and
output lifecycle: contained context-bound preparation, bounded cleanup after
cancellation, truthful owned-residue reporting, and no removal when ownership or
registration cannot be established. Reuse gofresh's public command containment;
do not introduce another process-group implementation or discard cleanup errors.

This is a separate lifecycle change from replacing the already-contained
measurement command's hand copy: worktree creation/removal has visible effects
and requires explicit failure/cleanup ordering, rather than the byte-command
wrapper's discard-on-failure result.
