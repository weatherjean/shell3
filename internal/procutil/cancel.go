// Package procutil holds the Unix process-lifecycle contract shared by shell
// execution surfaces.
package procutil

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// ConfigureGroupCancel gives the process group half of waitDelay to handle TERM,
// then kills remaining members. Escalation finishes before Wait returns; no
// detached timer can later signal a reused process ID. WaitDelay also bounds pipes.
func ConfigureGroupCancel(cmd *exec.Cmd, waitDelay time.Duration) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		group := -cmd.Process.Pid
		if err := syscall.Kill(group, syscall.SIGTERM); err != nil {
			if errors.Is(err, syscall.ESRCH) {
				return os.ErrProcessDone
			}
			return err
		}
		time.Sleep(waitDelay / 2)
		if err := syscall.Kill(group, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			return err
		}
		return nil
	}
	cmd.WaitDelay = waitDelay
}
