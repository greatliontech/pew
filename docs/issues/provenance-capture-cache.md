# Capture provenance once per module, not once per package

Lands: when `pew status`/`pew run` wall-clock proves slow, or the capture call sites are next
changed

## Fault (efficiency)

`cmd/pew/status.go` (`statusPackage`) and `cmd/pew/run.go` (`runPackage`) call
`provenance.Capture(p.Module.Dir)` once per *package*. Everything it gathers is per-module or
per-machine: go-git `Worktree.Status()` (the documented slow path on large repos, spec §11),
`go version` and `go env -json` subprocesses, and the machine-facts read. `pew status ./...` on a
50-package module performs 50 worktree statuses and 100 subprocess invocations for identical
answers.

## Resolution

Memoize `Capture` results keyed by module dir for the life of the command invocation (a small map
at the cmd layer, or a caching wrapper in `internal/provenance`). A working tree can change between
packages within one invocation only via a concurrent external writer, which the current
per-package calls do not defend against either — no behavior change.
