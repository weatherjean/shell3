//go:build unix

// Package modelproxy lazily brings up a model's proxy command.
//
// A model may declare a `run_proxy` shell command in shell3.yaml. The first time
// an agent activates that model, Spawner runs the command once — detached, in
// its own process group, fire-and-forget. The OS port bind acts as the mutex: a
// command spawned while a proxy is already listening simply fails to bind and
// exits harmlessly. Spawn failures are never fatal; the first request to the
// model surfaces any real problem as an ordinary API error.
//
// The proxy outlives shell3 — it is never killed or reaped on exit.
package modelproxy

import (
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/weatherjean/shell3/internal/applog"
)

const (
	logMaxBytes    = 10 * 1024 * 1024 // 10 MB
	logMaxArchives = 1
)

// Spawner starts run_proxy commands at most once per model name per process.
type Spawner struct {
	logDir string
	log    applog.Logger

	mu      sync.Mutex
	started map[string]bool
}

// New returns a Spawner that writes each proxy's stdout/stderr to
// <logDir>/proxy-<model>.log (rotated at 10 MB, 1 archive).
func New(logDir string, log applog.Logger) *Spawner {
	return &Spawner{logDir: logDir, log: log, started: map[string]bool{}}
}

// Ensure spawns command for model name if it has not already been attempted in
// this process. It is a no-op when command is empty. Spawn errors are logged and
// swallowed — Ensure never blocks on the proxy and never returns an error.
func (s *Spawner) Ensure(name, command string) {
	if command == "" {
		return
	}
	s.mu.Lock()
	if s.started[name] {
		s.mu.Unlock()
		return
	}
	s.started[name] = true
	s.mu.Unlock()

	s.spawn(name, command)
}

func (s *Spawner) spawn(name, command string) {
	logPath := filepath.Join(s.logDir, "proxy-"+sanitize(name)+".log")
	f, err := applog.OpenFile(logPath, logMaxBytes, logMaxArchives)
	if err != nil {
		// Proceed without redirection rather than dropping the proxy entirely.
		s.log.Warn("run_proxy: cannot open log; running without output capture",
			"model", name, "error", err)
	}

	cmd := exec.Command("sh", "-c", command)
	// Detach into its own process group so Ctrl+C / shell3 exit don't kill it.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if f != nil {
		cmd.Stdout = f
		cmd.Stderr = f
	}

	if err := cmd.Start(); err != nil {
		s.log.Warn("run_proxy: spawn failed", "model", name, "command", command, "error", err)
		if f != nil {
			_ = f.Close()
		}
		return
	}
	s.log.Debug("run_proxy: started", "model", name, "pid", cmd.Process.Pid)

	// Reap in the background so we don't leave a zombie while shell3 runs, and
	// close our copy of the log fd once the proxy exits. If shell3 exits first,
	// the detached proxy keeps running.
	go func() {
		_ = cmd.Wait()
		if f != nil {
			_ = f.Close()
		}
	}()
}

// sanitize maps a model name to a filesystem-safe log filename fragment.
func sanitize(name string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '.':
			return r
		default:
			return '_'
		}
	}, name)
}
