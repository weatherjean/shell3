// Package procutil holds the Unix process-lifecycle contract shared by shell
// execution surfaces.
package procutil

import (
	"os/exec"
	"syscall"
	"time"
)

// ConfigureGroupCancel puts cmd and its descendants in their own process
// group and signals the whole tree on context cancellation. waitDelay bounds
// how long Cmd.Wait can remain blocked by descendants that inherited its
// output pipes.
func ConfigureGroupCancel(cmd *exec.Cmd, waitDelay time.Duration) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	}
	cmd.WaitDelay = waitDelay
}
