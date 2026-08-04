# two go-invocation mechanisms carry divergent environment policy

`gotool.RunIn` inherits the caller's `$PWD` while `run.Execute` pins it
via `commandEnvironment`. Under a symlinked checkout with a truthful
exported `$PWD`, `go list` therefore reports the ALIAS module dir while
the measurement machinery works in resolved paths — the divergence the
observation wiring now bridges case-by-case (the measurement invocation
runs from the frame's resolved root, pinned by the symlinked-module
regression net). Unifying every go invocation on the pinned-environment
mechanism would make the resolved-dir premise hold by construction and
delete the per-site bridging.

Lands: the cross-tool train's dedicated pew chunk (gofresh docs/plans/cross-tool-train.md), after the gomutant tail.
