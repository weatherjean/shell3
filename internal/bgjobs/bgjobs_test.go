package bgjobs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// fakeRegistry is an in-memory bgjobs.Registry for tests. addErr, when set,
// makes Add fail (to exercise teardown).
type fakeRegistry struct {
	mu     sync.Mutex
	jobs   []Job
	addErr error
}

func (r *fakeRegistry) Add(j Job) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.addErr != nil {
		return r.addErr
	}
	r.jobs = append(r.jobs, j)
	return nil
}

func (r *fakeRegistry) List(workdir string) ([]Job, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Job, 0, len(r.jobs))
	for _, j := range r.jobs {
		if j.Workdir == workdir {
			out = append(out, j)
		}
	}
	return out, nil
}

func (r *fakeRegistry) Clear(workdir string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	kept := r.jobs[:0]
	n := 0
	for _, j := range r.jobs {
		if j.Workdir == workdir {
			n++
			continue
		}
		kept = append(kept, j)
	}
	r.jobs = kept
	return n, nil
}

var _ Registry = (*fakeRegistry)(nil)

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
	job, err := Start(&fakeRegistry{}, []string{"bash", "-c", "echo hi-from-bg"}, "echo hi-from-bg", wd, nil)
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

func TestStart_recordsInRegistry(t *testing.T) {
	wd := t.TempDir()
	reg := &fakeRegistry{}
	j1, err := Start(reg, []string{"bash", "-c", "true"}, "true", wd, nil)
	if err != nil {
		t.Fatal(err)
	}
	j2, err := Start(reg, []string{"bash", "-c", "true"}, "true", wd, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(j1.Log); os.Remove(j2.Log) })

	got, err := reg.List(wd)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 jobs, got %d", len(got))
	}
	if got[0].ID != j1.ID || got[1].ID != j2.ID {
		t.Fatalf("ids: %v", got)
	}
}

func TestStart_detached(t *testing.T) {
	wd := t.TempDir()
	job, err := Start(&fakeRegistry{}, []string{"bash", "-c", "sleep 30"}, "sleep 30", wd, nil)
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
	job, err := Start(&fakeRegistry{}, []string{"bash", "-c", "sleep 30 & wait"}, "sleep 30 & wait", wd, nil)
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

func TestStart_concurrentAddsDoNotRace(t *testing.T) {
	wd := t.TempDir()
	reg := &fakeRegistry{}
	const n = 10
	errs := make(chan error, n)
	jobs := make(chan Job, n)
	for i := 0; i < n; i++ {
		go func() {
			j, err := Start(reg, []string{"bash", "-c", "true"}, "true", wd, nil)
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
	got, err := reg.List(wd)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != n {
		t.Fatalf("want %d jobs, got %d", n, len(got))
	}
}

func TestStart_emptyCommandRejected(t *testing.T) {
	if _, err := Start(&fakeRegistry{}, nil, "", t.TempDir(), nil); err == nil {
		t.Fatal("expected error on empty command")
	}
}

func TestStart_killsProcessWhenPersistFails(t *testing.T) {
	wd := t.TempDir()
	reg := &fakeRegistry{addErr: fmt.Errorf("boom")}
	marker := filepath.Join(wd, "ran.marker")
	// If the spawned process is NOT killed, it survives the 1s sleep and creates
	// the marker. If Start kills it on persist failure, the marker never appears.
	cmd := fmt.Sprintf("sleep 1 && touch %q", marker)
	job, err := Start(reg, []string{"bash", "-c", cmd}, cmd, wd, nil)
	if err == nil {
		t.Fatal("expected persist error (Add fails), got nil")
	}
	if !strings.Contains(err.Error(), "persist") {
		t.Fatalf("error should be a persist failure, got: %v", err)
	}
	// On persist failure Start returns a zero-value Job, so no usable Log/PID
	// is handed back to the caller.
	if job.Log != "" {
		t.Fatalf("expected zero Job on failure, got %+v", job)
	}
	time.Sleep(2 * time.Second) // past the 1s sleep
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("spawned process was orphaned on persist failure (marker was created)")
	}
}

func TestKillAll(t *testing.T) {
	dir := t.TempDir()
	reg := &fakeRegistry{}
	job, err := Start(reg, []string{"bash", "-c", "sleep 60"}, "sleep 60", dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if syscall.Kill(job.PID, 0) != nil {
		t.Fatalf("job %d not alive after Start", job.PID)
	}
	n, err := KillAll(reg, dir)
	if err != nil {
		t.Fatal(err)
	}
	if n < 1 {
		t.Errorf("KillAll reported %d killed, want >=1", n)
	}
	if !waitDead(job.PID, 2*time.Second) {
		t.Errorf("job %d still alive after KillAll", job.PID)
	}
	if jobs, _ := reg.List(dir); len(jobs) != 0 {
		t.Errorf("registry not cleared: %v", jobs)
	}
}

func TestStartInjectsEnv(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "out.txt")
	cmd := `printf "%s" "$GREETING" > ` + out
	job, err := Start(&fakeRegistry{}, []string{"bash", "-c", cmd}, cmd, dir, []string{"GREETING=hi-env"})
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
