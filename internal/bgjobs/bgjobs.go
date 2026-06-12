//go:build unix

// Package bgjobs spawns detached background processes for the bash_bg tool
// and records them in a JSON registry the model can read with the regular
// bash tool (cat, jq, rm).
//
// Design notes:
//   - Logs live under /tmp/shell3/runs/<id>.log so they vanish on reboot.
//   - Registry lives at <workdir>/.shell3/bg.json so it is per-project.
//   - Processes are fully detached (Setpgid + Process.Release); the agent
//     manages liveness/cleanup via plain bash (kill, kill -0, rm).
package bgjobs

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/weatherjean/shell3/internal/paths"
)

// Job is one entry in bg.json. Fields are JSON-tagged for direct persistence.
type Job struct {
	ID        string    `json:"id"`
	PID       int       `json:"pid"`
	Cmd       string    `json:"cmd"`
	Log       string    `json:"log"`
	Workdir   string    `json:"workdir"`
	StartedAt time.Time `json:"started_at"`
}

// Registry is the on-disk shape of bg.json.
type Registry struct {
	Jobs []Job `json:"jobs"`
}

// fileLock serializes the read-modify-write of bg.json across goroutines
// WITHIN a single process; it does NOT guard cross-process races (multiple
// embedding hosts sharing one workdir can interleave, last rename wins). That
// is an accepted limitation: only a TRACKING entry is lost — the spawned
// process is detached and reaped independently — so flock was not added.
var fileLock sync.Mutex

// Start spawns argv (argv[0] with argv[1:] as args) in workdir, detached,
// returning the recorded Job; on return the process is fully released (bgjobs
// does not Wait on it).
//
// display is the human-readable command recorded as Job.Cmd in bg.json; it may
// differ from argv when wrap_bash swapped the runner.
//
// sinkPath is retained for signature stability but no longer drives anything:
// the sink-file notification mechanism has been retired in favor of the
// socket/SQLite-inbox transport (internal/notify, internal/socket). Callers pass
// "". Plain bg-job completions are no longer notified (durability is owed only
// to agent completion, which self-reports over the socket transport).
//
// notifyOnExit is also retained for signature stability and currently has no
// effect (it formerly gated the bg_done sink append).
//
// env, when non-empty, supplies extra KEY=VALUE entries appended to the
// inherited environment (os.Environ); used by command-template custom tools to
// pass their declared params + secrets to the background command. nil/empty
// means the job inherits only the host environment (the prior behavior).
func Start(argv []string, display, workdir string, env []string, sinkPath string, notifyOnExit bool) (Job, error) {
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
	// The child inherits this process's environment. The depth-1 gate is retired:
	// a spawned `shell3` subagent may itself delegate, and reports its result back
	// to its parent via the --parent-session pointer (socket/inbox transport).
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
	// Reap in a goroutine so the exited process leaves no zombie. We do NOT
	// Release(): that leaves the pid as a zombie forever, and `kill(pid, 0)`
	// reports zombies as alive, breaking the model's liveness checks. Plain
	// bg-job completions are no longer notified (the sink mechanism is retired);
	// the Wait() is solely to reap the zombie.
	go func() {
		_ = c.Wait()
	}()
	_ = sinkPath    // retained for signature stability; no longer used
	_ = notifyOnExit // retained for signature stability; no longer used

	job := Job{
		ID:        id,
		PID:       pid,
		Cmd:       display,
		Log:       logPath,
		Workdir:   workdir,
		StartedAt: time.Now().UTC(),
	}
	if err := appendJob(workdir, job); err != nil {
		// The process is spawned but unrecorded (PID never returned), so the
		// model can't manage it. Tear down the whole group and drop its log so a
		// failing persist can't orphan a live, unmanageable process; the reaping
		// goroutine then Wait()s it. The kill error is harmless to discard:
		// appendJob fails synchronously so pid reuse isn't a concern, and an
		// already-exited process just yields ESRCH.
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		_ = os.Remove(logPath)
		return Job{}, fmt.Errorf("persist: %w", err)
	}
	return job, nil
}

// exitCode extracts the process exit code from the error returned by
// (*exec.Cmd).Wait: nil → 0 (clean exit), an *exec.ExitError → the real code
// (including signal-derived codes via ExitCode), and any other error (e.g. an
// I/O failure waiting on the process) → -1 ("unknown").
func exitCode(err error) int {
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return -1
}

// newID returns "bg_<6 hex>".
func newID() (string, error) {
	var b [3]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "bg_" + hex.EncodeToString(b[:]), nil
}

// registryPath resolves the bg.json path for workdir, via the shared paths
// resolver so the location stays defined in one place (internal/paths).
func registryPath(workdir string) string {
	return paths.NewLocal(workdir).BGJobs
}

// LoadRegistry reads bg.json from workdir. Missing file → empty registry.
// Malformed JSON is returned as an error; callers may decide to back it up.
func LoadRegistry(workdir string) (Registry, error) {
	path := registryPath(workdir)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Registry{}, nil
		}
		return Registry{}, err
	}
	if len(data) == 0 {
		return Registry{}, nil
	}
	var r Registry
	if err := json.Unmarshal(data, &r); err != nil {
		return Registry{}, fmt.Errorf("parse bg.json: %w", err)
	}
	return r, nil
}

// appendJob locks, loads, appends, atomically rewrites bg.json. If the
// existing file is corrupt it is moved aside under a unique, timestamped
// bg.json.<nanos>.bak name and a fresh registry is written — preserves
// forensics (without clobbering earlier backups) and does not block new
// jobs. If the backup rename fails, appendJob returns the error rather than
// overwriting (and so destroying) the corrupt original.
func appendJob(workdir string, job Job) error {
	fileLock.Lock()
	defer fileLock.Unlock()
	if err := os.MkdirAll(filepath.Join(workdir, ".shell3"), 0o755); err != nil {
		return err
	}
	reg, err := LoadRegistry(workdir)
	if err != nil {
		// Corrupt file: back it up under a unique name and start fresh; on
		// rename failure surface the error rather than overwrite (see doc above).
		bak := registryPath(workdir) + fmt.Sprintf(".%d.bak", time.Now().UnixNano())
		if rerr := os.Rename(registryPath(workdir), bak); rerr != nil {
			return fmt.Errorf("back up corrupt bg.json: %w", rerr)
		}
		reg = Registry{}
	}
	reg.Jobs = append(reg.Jobs, job)
	return writeAtomic(registryPath(workdir), reg)
}

// KillAll terminates every tracked background job for workdir and clears the
// registry. Each job runs in its own process group (Setpgid at Start), so we
// signal the whole group (-pid) with SIGKILL. Already-dead PIDs are skipped.
// Returns the number of live jobs signalled.
func KillAll(workdir string) (int, error) {
	jobs, err := LoadRegistry(workdir)
	if err != nil {
		return 0, err
	}
	killed := 0
	for _, j := range jobs.Jobs {
		if j.PID <= 0 {
			continue
		}
		if syscall.Kill(j.PID, 0) != nil {
			continue // already gone
		}
		if err := syscall.Kill(-j.PID, syscall.SIGKILL); err == nil {
			killed++
		}
	}
	if err := clearRegistry(workdir); err != nil {
		return killed, fmt.Errorf("clear bg registry: %w", err)
	}
	return killed, nil
}

// clearRegistry removes the bg.json registry file for workdir (best-effort).
// A missing file is not an error: LoadRegistry treats absence as empty.
func clearRegistry(workdir string) error {
	fileLock.Lock()
	defer fileLock.Unlock()
	err := os.Remove(registryPath(workdir))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// writeAtomic marshals reg to path via a temp file + rename.
func writeAtomic(path string, reg Registry) error {
	data, err := json.MarshalIndent(reg, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".bg.json.*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}
