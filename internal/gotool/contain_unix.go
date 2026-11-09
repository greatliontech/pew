//go:build aix || android || darwin || dragonfly || freebsd || illumos || ios || linux || netbsd || openbsd || solaris

package gotool

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// contain is the Unix arm of Containment: the child leads its own
// process group (Setpgid), so a cancellation kills the group — every
// descendant that would otherwise outlive the leader and hold its
// pipes — and an already-empty group (ESRCH: Wait reaped the leader
// before the cancellation was noticed) is the process-done case.
func contain(cmd *exec.Cmd, c Containment) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.WaitDelay = c.waitDelay()
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return os.ErrProcessDone
		}
		return processDone(syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL))
	}
}

func processDone(err error) error {
	if errors.Is(err, syscall.ESRCH) {
		return os.ErrProcessDone
	}
	return err
}
