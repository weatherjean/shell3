//go:build unix

package bgjobs

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// alive returns true if pid responds to signal 0 (process exists and we
// can signal it). Used to assert detachment / liveness in tests.
func alive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

// waitDead polls until pid is gone or timeout. Returns true if dead.
func waitDead(pid int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !alive(pid) {
			return true
		}
		time.Sleep(20 * time.Millisecond)
	}
	return !alive(pid)
}

func TestStartWritesJobFiles(t *testing.T) {
	runsDir := t.TempDir()
	j, err := Start(runsDir, []string{"sh", "-c", "echo hi"}, "echo hi", runsDir, nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if j.PID == 0 || j.ID == "" {
		t.Fatalf("bad job %+v", j)
	}
	statusPath := filepath.Join(runsDir, "jobs", j.ID+".status")
	if _, err := os.Stat(statusPath); err != nil {
		t.Fatalf("status file missing: %v", err)
	}
	// Verify status file has expected fields
	data, err := os.ReadFile(statusPath)
	if err != nil {
		t.Fatalf("read status: %v", err)
	}
	var status struct {
		ID      string `json:"id"`
		PID     int    `json:"pid"`
		Cmd     string `json:"cmd"`
		Workdir string `json:"workdir"`
	}
	if err := json.Unmarshal(data, &status); err != nil {
		t.Fatalf("parse status: %v", err)
	}
	if status.ID != j.ID {
		t.Errorf("status id mismatch: got %q want %q", status.ID, j.ID)
	}
	if status.PID != j.PID {
		t.Errorf("status pid mismatch: got %d want %d", status.PID, j.PID)
	}
}

func TestStart_writesLogAndExits(t *testing.T) {
	runsDir := t.TempDir()
	wd := t.TempDir()
	job, err := Start(runsDir, []string{"bash", "-c", "echo hi-from-bg"}, "echo hi-from-bg", wd, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(job.ID, "bg_") || len(job.ID) != 9 {
		t.Fatalf("bad id: %q", job.ID)
	}
	if job.PID <= 0 {
		t.Fatalf("bad pid: %d", job.PID)
	}
	// Wait for the reaper goroutine to finish (process exit + status file write)
	// before reading the log and before returning so t.TempDir() cleanup does not
	// race the goroutine still writing into the temp dir.
	select {
	case <-job.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("reaper goroutine did not finish within 5s")
	}
	data, err := os.ReadFile(job.Log)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if !strings.Contains(string(data), "hi-from-bg") {
		t.Fatalf("log missing output: %q", data)
	}
}

func TestStart_detached(t *testing.T) {
	runsDir := t.TempDir()
	wd := t.TempDir()
	job, err := Start(runsDir, []string{"bash", "-c", "sleep 30"}, "sleep 30", wd, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Process should still be alive 200ms after Start returns.
	time.Sleep(200 * time.Millisecond)
	if !alive(job.PID) {
		t.Fatalf("process died prematurely: pid %d", job.PID)
	}
	// Kill the long-running process and wait for the reaper goroutine to finish
	// before the test returns. This prevents t.TempDir() cleanup from racing the
	// goroutine still writing into the temp dir.
	syscall.Kill(-job.PID, syscall.SIGKILL)
	select {
	case <-job.Done():
	case <-time.After(5 * time.Second):
		t.Error("reaper goroutine did not finish within 5s")
	}
}

func TestStart_grandchildKillablyViaPgid(t *testing.T) {
	runsDir := t.TempDir()
	wd := t.TempDir()
	job, err := Start(runsDir, []string{"bash", "-c", "sleep 30 & wait"}, "sleep 30 & wait", wd, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !alive(job.PID) {
		t.Fatalf("parent not alive")
	}
	// Kill entire process group; both bash and grandchild should die.
	if err := syscall.Kill(-job.PID, syscall.SIGKILL); err != nil {
		t.Fatalf("kill -pgid: %v", err)
	}
	if !waitDead(job.PID, 2*time.Second) {
		t.Fatalf("process group survived SIGKILL")
	}
	// Wait for the reaper goroutine to finish writing the exit-status file before
	// the test returns and t.TempDir() cleanup fires.
	select {
	case <-job.Done():
	case <-time.After(5 * time.Second):
		t.Error("reaper goroutine did not finish within 5s")
	}
}

func TestStart_concurrentAddsDoNotRace(t *testing.T) {
	runsDir := t.TempDir()
	wd := t.TempDir()
	const n = 10
	errs := make(chan error, n)
	jobsCh := make(chan Job, n)
	for i := 0; i < n; i++ {
		go func() {
			j, err := Start(runsDir, []string{"bash", "-c", "true"}, "true", wd, nil)
			jobsCh <- j
			errs <- err
		}()
	}
	var allJobs []Job
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("start %d: %v", i, err)
		}
		allJobs = append(allJobs, <-jobsCh)
	}

	// Wait for all reaper goroutines to finish before asserting or returning.
	// Without this, fast-exiting "true" processes have their reaper goroutines
	// still running when t.TempDir() cleanup fires, causing "directory not empty"
	// errors.
	for _, j := range allJobs {
		if j.Done() != nil {
			select {
			case <-j.Done():
			case <-time.After(5 * time.Second):
				t.Error("reaper goroutine did not finish within 5s")
			}
		}
	}

	got, err := List(runsDir)
	if err != nil {
		t.Fatal(err)
	}
	// Some may have exited and been pruned by List; but we expect at least some
	// and all n status files to have been created.
	_ = got
	entries, err := filepath.Glob(filepath.Join(runsDir, "jobs", "*.status"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != n {
		t.Fatalf("want %d status files, got %d", n, len(entries))
	}
}

func TestStart_emptyCommandRejected(t *testing.T) {
	if _, err := Start(t.TempDir(), nil, "", t.TempDir(), nil); err == nil {
		t.Fatal("expected error on empty command")
	}
}

func TestKillAll(t *testing.T) {
	runsDir := t.TempDir()
	dir := t.TempDir()
	job, err := Start(runsDir, []string{"bash", "-c", "sleep 60"}, "sleep 60", dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if syscall.Kill(job.PID, 0) != nil {
		t.Fatalf("job %d not alive after Start", job.PID)
	}
	n, err := KillAll(runsDir, dir)
	if err != nil {
		t.Fatal(err)
	}
	if n < 1 {
		t.Errorf("KillAll reported %d killed, want >=1", n)
	}
	if !waitDead(job.PID, 2*time.Second) {
		t.Errorf("job %d still alive after KillAll", job.PID)
	}
	// Status files for this workdir should be removed.
	entries, _ := filepath.Glob(filepath.Join(runsDir, "jobs", "*.status"))
	if len(entries) != 0 {
		t.Errorf("status files not cleaned up: %v", entries)
	}
}

func TestStartInjectsEnv(t *testing.T) {
	runsDir := t.TempDir()
	dir := t.TempDir()
	out := filepath.Join(dir, "out.txt")
	cmd := `printf "%s" "$GREETING" > ` + out
	job, err := Start(runsDir, []string{"bash", "-c", cmd}, cmd, dir, []string{"GREETING=hi-env"})
	if err != nil {
		t.Fatal(err)
	}
	// wait for the detached job to finish writing
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if b, _ := os.ReadFile(out); string(b) == "hi-env" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = job
	t.Fatalf("env var not visible to background job; out=%q", readFile(out))
}

func readFile(p string) string { b, _ := os.ReadFile(p); return string(b) }
