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
// WITHIN a single process. It does NOT guard against cross-process races:
// because pkg/shell3 is a public embeddable library, multiple host
// processes (or multiple shell3 binaries) can target the same workdir's
// .shell3/bg.json concurrently. In that case two appendJob calls in
// different processes can interleave and the last atomic rename wins,
// silently dropping the other process's appended job. This is an accepted
// limitation: the only lost state is a bg.json TRACKING entry — the
// spawned process itself is already detached and reaped independently — so
// flock was deliberately not added here.
var fileLock sync.Mutex

// Start spawns command in workdir detached, returning the recorded Job.
// On return the process is fully released — bgjobs does not Wait on it.
func Start(command, workdir string) (Job, error) {
	if command == "" {
		return Job{}, fmt.Errorf("command is required")
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

	c := exec.Command("bash", "-c", command)
	c.Dir = workdir
	c.Stdin = devNull
	c.Stdout = logFile
	c.Stderr = logFile
	// Setpgid: new process group so a single kill -- -pgid takes down
	// the whole tree (bash + any grandchildren). We do not Setsid: it
	// requires extra privileges in some sandboxes and is not needed
	// because shell3 redirects stdio to a file (so tty signals can't
	// reach the job anyway).
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := c.Start(); err != nil {
		return Job{}, fmt.Errorf("start: %w", err)
	}
	pid := c.Process.Pid
	// Reap in a goroutine so the kernel does not leave a zombie when the
	// process exits. We do NOT call Release(): Release would leave the
	// pid in zombie state forever (Go is the parent but never waits) and
	// `kill(pid, 0)` reports zombies as alive, breaking liveness checks
	// the model relies on.
	go func() { _ = c.Wait() }()

	job := Job{
		ID:        id,
		PID:       pid,
		Cmd:       command,
		Log:       logPath,
		Workdir:   workdir,
		StartedAt: time.Now().UTC(),
	}
	if err := appendJob(workdir, job); err != nil {
		// The process is spawned but unrecorded: the model can't find or kill it
		// (the PID is never returned). Tear down the whole group (-pid, enabled
		// by Setpgid above) and drop its log so a failing-disk persist can't
		// orphan a live, unmanageable process. The reaping goroutine above then
		// Wait()s it, leaving no zombie. The kill error is discarded: appendJob
		// fails synchronously (microseconds after Start), so pid reuse is not a
		// practical concern, and if the process already exited Kill just returns
		// ESRCH — harmless either way.
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

// registryPath resolves the bg.json path for workdir.
func registryPath(workdir string) string {
	return filepath.Join(workdir, ".shell3", "bg.json")
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
		// Back up the bad file under a unique name, then start fresh. A
		// timestamped suffix keeps successive corruptions from clobbering
		// each other's forensic copy. If the rename fails we must NOT
		// proceed to overwrite the corrupt original (that would destroy the
		// only copy of the bad data); surface the error instead.
		bak := registryPath(workdir) + fmt.Sprintf(".%d.bak", time.Now().UnixNano())
		if rerr := os.Rename(registryPath(workdir), bak); rerr != nil {
			return fmt.Errorf("back up corrupt bg.json: %w", rerr)
		}
		reg = Registry{}
	}
	reg.Jobs = append(reg.Jobs, job)
	return writeAtomic(registryPath(workdir), reg)
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
