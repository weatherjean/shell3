//go:build unix

// Package bgjobs spawns detached background processes for the bash_bg tool
// and records them in a Registry (backed by the canonical store via
// internal/jobstore) the model can list with `shell3 jobs`.
//
// Design notes:
//   - Logs live under /tmp/shell3/runs/<id>.log so they vanish on reboot.
//   - The Registry records jobs per-workdir; bgjobs stays decoupled from the
//     store (no import cycle) behind the Registry interface.
//   - Processes are fully detached (Setpgid + reaped via Wait); the agent
//     manages liveness/cleanup via plain bash (kill, kill -0).
package bgjobs

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/weatherjean/shell3/internal/paths"
)

// Registry records and lists spawned background jobs. Implemented by
// internal/jobstore over the canonical store.
type Registry interface {
	Add(Job) error
	List(workdir string) ([]Job, error)
	Clear(workdir string) (int, error)
}

// Job is one tracked background process.
type Job struct {
	ID        string
	PID       int
	Cmd       string
	Log       string
	Workdir   string
	StartedAt time.Time
}

// Start spawns argv (argv[0] with argv[1:] as args) in workdir, detached,
// recording the Job in reg; on return the process is fully released (bgjobs
// does not Wait on it synchronously).
//
// display is the human-readable command recorded as Job.Cmd; it may differ from
// argv when wrap_bash swapped the runner.
//
// env, when non-empty, supplies extra KEY=VALUE entries appended to the
// inherited environment (os.Environ); used by command-template custom tools to
// pass their declared params + secrets to the background command. nil/empty
// means the job inherits only the host environment.
func Start(reg Registry, argv []string, display, workdir string, env []string) (Job, error) {
	if len(argv) == 0 {
		return Job{}, fmt.Errorf("argv is required")
	}
	if workdir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return Job{}, fmt.Errorf("workdir resolve: %w", err)
		}
		workdir = wd
	}
	id, err := newID()
	if err != nil {
		return Job{}, err
	}
	if err := os.MkdirAll(paths.BGLogDir(), 0o755); err != nil {
		return Job{}, fmt.Errorf("mkdir log dir: %w", err)
	}
	logPath := paths.BGLogPath(id)
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return Job{}, fmt.Errorf("open log: %w", err)
	}
	defer logFile.Close()
	devNull, err := os.OpenFile(os.DevNull, os.O_RDONLY, 0)
	if err != nil {
		return Job{}, fmt.Errorf("open /dev/null: %w", err)
	}
	defer devNull.Close()

	c := exec.Command(argv[0], argv[1:]...)
	c.Dir = workdir
	// The child inherits this process's environment. A spawned shell3 subagent
	// may itself delegate, reporting its result back to its parent via the
	// --parent-session pointer (socket/inbox transport).
	c.Env = append([]string(nil), os.Environ()...)
	if len(env) > 0 {
		c.Env = append(c.Env, env...)
	}
	c.Stdin = devNull
	c.Stdout = logFile
	c.Stderr = logFile
	// Setpgid: new process group so kill -- -pgid takes down the whole tree.
	// We don't Setsid (needs privileges in some sandboxes; unneeded since stdio
	// is redirected to a file, so tty signals can't reach the job).
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := c.Start(); err != nil {
		return Job{}, fmt.Errorf("start: %w", err)
	}
	pid := c.Process.Pid
	// Reap in a goroutine so the exited process leaves no zombie. Release()
	// would leave the pid a zombie, and kill(pid, 0) reports zombies as alive,
	// breaking the model's liveness checks; the Wait() reaps it.
	go func() {
		_ = c.Wait()
	}()

	job := Job{
		ID:        id,
		PID:       pid,
		Cmd:       display,
		Log:       logPath,
		Workdir:   workdir,
		StartedAt: time.Now().UTC(),
	}
	if err := reg.Add(job); err != nil {
		// The process is spawned but unrecorded (PID never returned), so the
		// model can't manage it. Tear down the whole group and drop its log so a
		// failing persist can't orphan a live, unmanageable process; the reaping
		// goroutine then Wait()s it. The kill error is safe to discard: Add
		// fails synchronously so pid reuse isn't a concern, and an already-exited
		// process just yields ESRCH.
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		_ = os.Remove(logPath)
		return Job{}, fmt.Errorf("persist: %w", err)
	}
	return job, nil
}

// newID returns "bg_<6 hex>".
func newID() (string, error) {
	var b [3]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "bg_" + hex.EncodeToString(b[:]), nil
}

// KillAll signals every tracked job for workdir (whole process group) and clears
// the registry. Returns the number of live jobs signalled.
func KillAll(reg Registry, workdir string) (int, error) {
	jobs, err := reg.List(workdir)
	if err != nil {
		return 0, err
	}
	killed := 0
	for _, j := range jobs {
		if j.PID <= 0 || syscall.Kill(j.PID, 0) != nil {
			continue
		}
		if syscall.Kill(-j.PID, syscall.SIGKILL) == nil {
			killed++
		}
	}
	if _, err := reg.Clear(workdir); err != nil {
		return killed, fmt.Errorf("clear registry: %w", err)
	}
	return killed, nil
}
