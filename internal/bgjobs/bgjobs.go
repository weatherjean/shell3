//go:build unix

// Package bgjobs spawns detached background processes for the bash_bg tool
// and records them as files under runsDir/jobs/ so they survive process restart.
//
// Design notes:
//   - Each job writes stdout+stderr to runsDir/jobs/<id>.jsonl.
//   - A <id>.status JSON file (id, pid, cmd, workdir, started_at) is written by
//     Start; the reaping goroutine updates it with an exit code on completion.
//   - List reads *.status and prunes entries whose pid is no longer alive via
//     proc.Alive.
//   - Processes are fully detached (Setpgid + reaped via Wait); the agent
//     manages liveness/cleanup via plain bash (kill, kill -0).
package bgjobs

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/weatherjean/shell3/internal/proc"
)

// reaperRegistry maps pid (int) → done chan struct{} so KillAll can wait for
// the reaper goroutine to complete before removing files, eliminating the race
// where the reaper re-creates a status file KillAll has already deleted.
var reaperRegistry sync.Map // map[int]chan struct{}

// Job is one tracked background process.
type Job struct {
	ID        string
	PID       int
	Cmd       string
	Log       string
	Workdir   string
	StartedAt time.Time

	// done is closed by the internal reaper goroutine once it has finished
	// writing (or skipping) the exit-status file. Tests and KillAll use this
	// to synchronize with the goroutine before cleanup.
	done <-chan struct{}
}

// Done returns a channel that is closed when the reaper goroutine for this job
// has finished (i.e., the process has exited and its status file has been
// finalised). It is safe to use for test synchronization and KillAll ordering.
func (j Job) Done() <-chan struct{} { return j.done }

// statusFile is the on-disk representation of a job.
type statusFile struct {
	ID        string    `json:"id"`
	PID       int       `json:"pid"`
	Cmd       string    `json:"cmd"`
	Log       string    `json:"log"`
	Workdir   string    `json:"workdir"`
	StartedAt time.Time `json:"started_at"`
	ExitCode  *int      `json:"exit_code,omitempty"`
}

// jobsDir returns the jobs sub-directory under runsDir.
func jobsDir(runsDir string) string { return filepath.Join(runsDir, "jobs") }

// statusPath returns the path for a job's status file.
func statusPath(runsDir, id string) string {
	return filepath.Join(jobsDir(runsDir), id+".status")
}

// logPath returns the path for a job's output log.
// LogPath returns the path to a job's combined stdout/stderr log, so callers
// outside the package can read a job's output by id.
func LogPath(runsDir, id string) string { return logPath(runsDir, id) }

func logPath(runsDir, id string) string {
	return filepath.Join(jobsDir(runsDir), id+".jsonl")
}

// writeStatus writes (or overwrites) the status file for a job.
func writeStatus(runsDir string, sf statusFile) error {
	data, err := json.Marshal(sf)
	if err != nil {
		return err
	}
	return os.WriteFile(statusPath(runsDir, sf.ID), data, 0o644)
}

// Start spawns argv (argv[0] with argv[1:] as args) in workdir, detached.
// display is the human-readable command recorded as Job.Cmd; it may differ from
// argv when wrap_bash swapped the runner.
//
// env, when non-empty, supplies extra KEY=VALUE entries appended to the
// inherited environment (os.Environ); used by command-template custom tools to
// pass their declared params + secrets to the background command. nil/empty
// means the job inherits only the host environment.
//
// Job output (stdout+stderr) is written to runsDir/jobs/<id>.jsonl. A status
// JSON file is written to runsDir/jobs/<id>.status.
func Start(runsDir string, argv []string, display, workdir string, env []string) (Job, error) {
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
	dir := jobsDir(runsDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Job{}, fmt.Errorf("mkdir jobs dir: %w", err)
	}
	lp := logPath(runsDir, id)
	logFile, err := os.OpenFile(lp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
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
	c.Env = append([]string(nil), os.Environ()...)
	if len(env) > 0 {
		c.Env = append(c.Env, env...)
	}
	c.Stdin = devNull
	c.Stdout = logFile
	c.Stderr = logFile
	// Setpgid: new process group so kill -- -pgid takes down the whole tree.
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := c.Start(); err != nil {
		return Job{}, fmt.Errorf("start: %w", err)
	}
	pid := c.Process.Pid
	startedAt := time.Now().UTC()

	sf := statusFile{
		ID:        id,
		PID:       pid,
		Cmd:       display,
		Log:       lp,
		Workdir:   workdir,
		StartedAt: startedAt,
	}
	if err := writeStatus(runsDir, sf); err != nil {
		// Status file write failed: kill the spawned process so it doesn't become
		// an unmanageable orphan, then remove the log file.
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		_ = os.Remove(lp)
		return Job{}, fmt.Errorf("persist: %w", err)
	}

	// done is closed by the reaper goroutine once it has finished all file I/O.
	// Register in the global registry keyed by pid so KillAll can look it up.
	doneCh := make(chan struct{})
	reaperRegistry.Store(pid, doneCh)

	// Reap in a goroutine so the exited process leaves no zombie. Write the exit
	// code into the status file on completion, then signal done.
	go func() {
		defer func() {
			reaperRegistry.Delete(pid)
			close(doneCh)
		}()
		werr := c.Wait()
		code := 0
		if werr != nil {
			if ee, ok := werr.(*exec.ExitError); ok {
				code = ee.ExitCode()
			} else {
				code = -1
			}
		}
		// Only write the exit status if the status file still exists. If KillAll
		// (or a test) has already removed it, skip the write so we do not
		// resurrect a deleted file.
		sp := statusPath(runsDir, sf.ID)
		if _, serr := os.Stat(sp); serr == nil {
			sf2 := sf
			sf2.ExitCode = &code
			_ = writeStatus(runsDir, sf2)
		}
	}()

	job := Job{
		ID:        id,
		PID:       pid,
		Cmd:       display,
		Log:       lp,
		Workdir:   workdir,
		StartedAt: startedAt,
		done:      doneCh,
	}
	return job, nil
}

// List reads runsDir/jobs/*.status, prunes entries whose pid is no longer
// alive (via proc.Alive), and returns the remaining live jobs.
func List(runsDir string) ([]Job, error) {
	pattern := filepath.Join(jobsDir(runsDir), "*.status")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("glob status files: %w", err)
	}
	var out []Job
	for _, path := range matches {
		data, err := os.ReadFile(path)
		if err != nil {
			continue // file may have been removed concurrently
		}
		var sf statusFile
		if err := json.Unmarshal(data, &sf); err != nil {
			continue
		}
		if !proc.Alive(sf.PID) {
			continue
		}
		// Recover the done channel from the registry if the pid is still managed
		// by a reaper in this process.
		var done <-chan struct{}
		if v, ok := reaperRegistry.Load(sf.PID); ok {
			done = v.(chan struct{})
		}
		out = append(out, Job{
			ID:        sf.ID,
			PID:       sf.PID,
			Cmd:       sf.Cmd,
			Log:       sf.Log,
			Workdir:   sf.Workdir,
			StartedAt: sf.StartedAt,
			done:      done,
		})
	}
	return out, nil
}

// KillAll signals every live job whose workdir matches (whole process group),
// waits for each job's reaper goroutine to finish (so no reaper can re-create
// a status file after we remove it), then removes their status+log files.
// Returns the number of live jobs signalled.
func KillAll(runsDir, workdir string) (int, error) {
	pattern := filepath.Join(jobsDir(runsDir), "*.status")
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return 0, fmt.Errorf("glob status files: %w", err)
	}
	type victim struct {
		sf     statusFile
		path   string
		doneCh <-chan struct{}
	}
	var victims []victim
	killed := 0
	for _, path := range matches {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var sf statusFile
		if err := json.Unmarshal(data, &sf); err != nil {
			continue
		}
		if sf.Workdir != workdir {
			continue
		}
		var doneCh <-chan struct{}
		if v, ok := reaperRegistry.Load(sf.PID); ok {
			doneCh = v.(chan struct{})
		}
		if sf.PID > 0 && proc.Alive(sf.PID) {
			if syscall.Kill(-sf.PID, syscall.SIGKILL) == nil {
				killed++
			}
		}
		victims = append(victims, victim{sf: sf, path: path, doneCh: doneCh})
	}

	// Wait for every reaper goroutine to finish before removing files.
	// This prevents the reaper from re-creating a status file after we delete it.
	// Use a 5-second timeout per victim to avoid deadlock if something goes wrong.
	for _, v := range victims {
		if v.doneCh != nil {
			select {
			case <-v.doneCh:
			case <-time.After(5 * time.Second):
			}
		}
	}

	// Now it is safe to remove status and log files — no reaper will write them.
	for _, v := range victims {
		_ = os.Remove(v.path)
		_ = os.Remove(logPath(runsDir, v.sf.ID))
	}
	return killed, nil
}

// Kill signals one job by id with SIGTERM to its whole process group, allowing a
// graceful shutdown. Unlike KillAll it does NOT remove the status/log files: the
// reaper records the exit code and List prunes the job once its pid is dead, so
// the job's output stays readable after the kill. Returns an error when no job
// with that id exists or the signal fails; an already-exited job is a no-op
// (nil), since there is nothing to signal.
func Kill(runsDir, id string) error {
	path := statusPath(runsDir, id)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("no such job %q", id)
		}
		return fmt.Errorf("read status %q: %w", id, err)
	}
	var sf statusFile
	if err := json.Unmarshal(data, &sf); err != nil {
		return fmt.Errorf("parse status %q: %w", id, err)
	}
	if sf.PID > 0 && proc.Alive(sf.PID) {
		if err := syscall.Kill(-sf.PID, syscall.SIGTERM); err != nil {
			return fmt.Errorf("signal job %q (pid %d): %w", id, sf.PID, err)
		}
	}
	return nil
}

// newID returns "bg_<6 hex>".
func newID() (string, error) {
	var b [3]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "bg_" + hex.EncodeToString(b[:]), nil
}
