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

func TestStart_writesLogAndExits(t *testing.T) {
	wd := t.TempDir()
	job, err := Start("echo hi-from-bg", wd)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(job.ID, "bg_") || len(job.ID) != 9 {
		t.Fatalf("bad id: %q", job.ID)
	}
	if job.PID <= 0 {
		t.Fatalf("bad pid: %d", job.PID)
	}
	// Wait for process to finish + log flush.
	waitDead(job.PID, 2*time.Second)
	data, err := os.ReadFile(job.Log)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if !strings.Contains(string(data), "hi-from-bg") {
		t.Fatalf("log missing output: %q", data)
	}
	t.Cleanup(func() { os.Remove(job.Log) })
}

func TestStart_appendsToRegistry(t *testing.T) {
	wd := t.TempDir()
	j1, err := Start("true", wd)
	if err != nil {
		t.Fatal(err)
	}
	j2, err := Start("true", wd)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(j1.Log); os.Remove(j2.Log) })

	reg, err := LoadRegistry(wd)
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Jobs) != 2 {
		t.Fatalf("want 2 jobs, got %d", len(reg.Jobs))
	}
	if reg.Jobs[0].ID != j1.ID || reg.Jobs[1].ID != j2.ID {
		t.Fatalf("ids: %v", reg.Jobs)
	}
}

func TestStart_detached(t *testing.T) {
	wd := t.TempDir()
	job, err := Start("sleep 30", wd)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		syscall.Kill(-job.PID, syscall.SIGKILL)
		os.Remove(job.Log)
	})
	// Process should still be alive 200ms after Start returns.
	time.Sleep(200 * time.Millisecond)
	if !alive(job.PID) {
		t.Fatalf("process died prematurely: pid %d", job.PID)
	}
}

func TestStart_grandchildKillablyViaPgid(t *testing.T) {
	wd := t.TempDir()
	// `setsid bash` ensures the inner sleep inherits the pgid.
	job, err := Start("sleep 30 & wait", wd)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(job.Log) })
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
}

func TestStart_corruptRegistryRecovers(t *testing.T) {
	wd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(wd, ".shell3"), 0o755); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(wd, ".shell3", "bg.json")
	if err := os.WriteFile(bad, []byte("{not valid json"), 0o644); err != nil {
		t.Fatal(err)
	}
	job, err := Start("true", wd)
	if err != nil {
		t.Fatalf("start should recover from corrupt bg.json: %v", err)
	}
	t.Cleanup(func() { os.Remove(job.Log) })
	if _, err := os.Stat(bad + ".bak"); err != nil {
		t.Fatalf("expected backup file at %s.bak", bad)
	}
	reg, err := LoadRegistry(wd)
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Jobs) != 1 {
		t.Fatalf("registry should hold 1 job, got %d", len(reg.Jobs))
	}
}

func TestStart_concurrentAppendsDoNotRace(t *testing.T) {
	wd := t.TempDir()
	const n = 10
	errs := make(chan error, n)
	jobs := make(chan Job, n)
	for i := 0; i < n; i++ {
		go func() {
			j, err := Start("true", wd)
			jobs <- j
			errs <- err
		}()
	}
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Fatalf("start %d: %v", i, err)
		}
	}
	close(jobs)
	t.Cleanup(func() {
		for j := range jobs {
			os.Remove(j.Log)
		}
	})
	reg, err := LoadRegistry(wd)
	if err != nil {
		t.Fatal(err)
	}
	if len(reg.Jobs) != n {
		t.Fatalf("want %d jobs, got %d", n, len(reg.Jobs))
	}
}

func TestStart_emptyCommandRejected(t *testing.T) {
	if _, err := Start("", t.TempDir()); err == nil {
		t.Fatal("expected error on empty command")
	}
}

func TestRegistry_jsonShape(t *testing.T) {
	wd := t.TempDir()
	job, err := Start("true", wd)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(job.Log) })
	data, err := os.ReadFile(registryPath(wd))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("bg.json not valid json: %v", err)
	}
	if _, ok := raw["jobs"]; !ok {
		t.Fatalf("missing 'jobs' key: %s", data)
	}
}
