# Contained Child Process Group Orphans on Parent `SIGKILL` and Holds Inherited Locks

- **Status:** Open
- **Governing contract:** [`docs/specs/spec.md`](../specs/spec.md) (§9, §13 `REQ-pew-query-containment` / `REQ-pew-interruption`), [`docs/guidance.md`](../guidance.md)
- **Lands:** awaits triage

## Problem

`internal/gotool/contain_unix.go` (`contain`) configures `go`, `taskset`, and compiled benchmark binaries (`internal/run/run.go` `ExecuteBinaryContext` / `ExecuteContext` / `ExecuteDiagnostic` / `BuildContext` -> `runCommandDiagnostic`, covering both `pew run`'s `/tmp/pew-testbin-*` and `pew ab`'s `.pew-ab-bin-*/{a,b}.test`) with `syscall.SysProcAttr{Setpgid: true}` so userspace context cancellation (`cmd.Cancel`) can `SIGKILL` the child's process group (`-cmd.Process.Pid`). Meanwhile, `runCommandDiagnostic` invokes `runner.Program(...)` followed by bare `cmd.Run()` rather than `internal/gotool/collect_linux.go` (`runCollected` / `runContained`).

When `pew` (or its outer `mlock` process group) is terminated by `SIGKILL` (for example, an external task supervisor aborting a background run with `SIGKILL`, `kill -9`, or OOM termination):

1. `pew`'s userspace `signal.NotifyContext` / `cmd.Cancel` never runs on `SIGKILL`.
2. Because `Setpgid: true` moved the child benchmark binary out of `pew`'s process group without Linux `Pdeathsig: syscall.SIGKILL`, a process-group `SIGKILL` sent to `-pew.Pgid` (or to `pew` directly) does not reach the child process group.
3. Although `internal/run/harnessflags.go` (`spec.md` §9) forwards `-test.timeout=10m0s` as a harness flag, Go's stdlib `testing.(*M).Run` calls `m.stopAlarm()` immediately after `runTests` and before `runBenchmarks`, so `-test.timeout` never bounds benchmark execution. A livelocked benchmark inside `pew-testbin-*` therefore runs indefinitely reparented to PID 1 while retaining inherited file descriptors (`>= 3`) — including `mlock quiet`'s open exclusive `flock(2)` descriptor on `~/.machine.lock`, wedging machine-wide lock acquisition after `pew` has already exited and leaving `/tmp/pew-testbin-*` and `/tmp/pew-testlog-*` behind.

## Resolution Scope

1. On Linux, arm `SysProcAttr.Pdeathsig = syscall.SIGKILL` alongside `Setpgid: true` **only** inside a `gotool`-owned execution entry point (`internal/gotool/collect_linux.go` / `Runner.Run` or a shared contained runner, never unconditionally on unowned `*exec.Cmd` values returned by `Runner.Program` or `Runner.PrepareCommand` where external callers such as `gofresh` drive `Start`/`Wait`), holding `runtime.LockOSThread()` for the **entire lifetime of the child** — from before `cmd.Start()` until after child `Wait` completes (or on `cmd.Start()` failure) — before calling `runtime.UnlockOSThread()` (`go.dev/issue/27505`, `PR_SET_PDEATHSIG(2const)`). Route `internal/run/run.go` (`runCommandDiagnostic`) through that contained execution path, and ensure any grandchildren in the child's process group or inherited non-standard file descriptors are also bounded cleanly.
2. Reconcile `docs/specs/spec.md` §9's `-test.timeout=10m0s` prose and `internal/run` benchmark execution with the fact that Go disarms `-test.timeout` before `testing.runBenchmarks` (for example via an optional or default per-arm wall-clock execution timeout).
3. Add a Linux integration test in `internal/gotool` / `internal/run` verifying that `SIGKILL`ing a parent `pew` / contained-runner process mid-benchmark immediately terminates the `Setpgid` child and releases an inherited `flock(2)` descriptor.
