//go:build unix

// Package proc holds tiny OS-process helpers shared across packages.
package proc

import "syscall"

// Alive reports whether pid names a running process. Signal 0 probes without
// delivering: nil (alive) or EPERM (alive, not ours) → alive; ESRCH → gone.
func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}
