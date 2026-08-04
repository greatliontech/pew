# pew's own recordings dirty the tree for later runs in a session

**Lands:** the cross-tool train's dedicated pew chunk (gofresh
docs/plans/cross-tool-train.md), after the gomutant tail.

A multi-package recording session self-poisons its provenance: the
first `pew run` writes recording files under `benchmarks/`, and every
later run in the same session sees a dirty worktree — its recordings
are born `dirty: true` and refused as stat baselines, though the
MEASURED tree (the source closure) is exactly the committed commit.
Observed in tugboat: a six-arm two-package batch recorded the first
package clean and the second dirty purely from the first package's
output files; the operator had to interleave commits between package
runs to keep provenance intact.

The dirtiness judgment should exclude the recording store itself
(the configured bench-dir): recordings are pew's own outputs, not
part of the measured subject. Anything else under the tree staying
dirty-relevant is correct.
