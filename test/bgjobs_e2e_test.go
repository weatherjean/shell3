//go:build unix

package test

import (
	"os"
	"syscall"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/bgjobs"
	"github.com/weatherjean/shell3/internal/proc"
)

// TestBgJobs_LifecycleAndPrune proves the file-native bg-job lifecycle: a real
// tracked background process appears in bgjobs.List, and once its process is
// killed the next bgjobs.List prunes it (so it stops being listed) because List
// drops entries whose pid is no longer alive.
//
// The old version drove this through the `shell3 jobs` CLI subcommand against a
// SQLite-backed registry; that subcommand and the store no longer exist
// (fire-and-forget, file-native). The surviving mechanism is the bgjobs file
// API (Start/List over runsDir/jobs/*.status), exercised here against a real
// process and a real temp runsDir.
func TestBgJobs_LifecycleAndPrune(t *testing.T) {
	runsDir := t.TempDir()
	workDir, err := os.MkdirTemp("/tmp", "bgjob")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(workDir) })

	// Spawn a REAL long-lived bg process, tracked via the file store. A generous
	// sleep keeps the job live across the List calls even on a slow runner.
	job, err := bgjobs.Start(runsDir, []string{"sleep", "120"}, "sleep 120", workDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Ensure cleanup kills the group even if asserts fail.
	t.Cleanup(func() { _ = syscall.Kill(-job.PID, syscall.SIGKILL) })

	listed := func() bool {
		jobs, lerr := bgjobs.List(runsDir)
		if lerr != nil {
			t.Fatalf("bgjobs.List: %v", lerr)
		}
		for _, j := range jobs {
			if j.ID == job.ID {
				return true
			}
		}
		return false
	}

	// 1) The live job is listed.
	if !listed() {
		t.Fatalf("bgjobs.List did not list live job %s", job.ID)
	}

	// 2) Kill the whole group; wait until the pid is actually gone.
	_ = syscall.Kill(-job.PID, syscall.SIGKILL)
	deadline := time.Now().Add(10 * time.Second)
	for proc.Alive(job.PID) && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if proc.Alive(job.PID) {
		t.Fatalf("job pid %d still alive after SIGKILL", job.PID)
	}
	// Wait for the reaper goroutine so it can't re-touch files mid-assertion.
	select {
	case <-job.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("reaper goroutine did not finish within 5s")
	}

	// 3) Next bgjobs.List must PRUNE it (no longer listed). Poll because
	//    reaping/visibility can lag slightly.
	deadline = time.Now().Add(10 * time.Second)
	pruned := false
	for time.Now().Before(deadline) {
		if !listed() {
			pruned = true
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !pruned {
		t.Fatalf("dead job %s was not pruned from bgjobs.List", job.ID)
	}
}

// TestBgJobs_KillAllOverStore proves bgjobs.KillAll, operating over the
// file-native store, kills the tracked OS process(es) for a workdir and removes
// their status+log files.
func TestBgJobs_KillAllOverStore(t *testing.T) {
	runsDir := t.TempDir()
	workDir, err := os.MkdirTemp("/tmp", "bgkill")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(workDir) })

	job, err := bgjobs.Start(runsDir, []string{"sleep", "30"}, "sleep 30", workDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(-job.PID, syscall.SIGKILL) })
	if !proc.Alive(job.PID) {
		t.Fatal("spawned job not alive")
	}

	killed, err := bgjobs.KillAll(runsDir, workDir)
	if err != nil {
		t.Fatal(err)
	}
	if killed < 1 {
		t.Fatalf("KillAll reported %d killed, want >=1", killed)
	}

	// process dies:
	deadline := time.Now().Add(10 * time.Second)
	for proc.Alive(job.PID) && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if proc.Alive(job.PID) {
		t.Fatalf("pid %d alive after KillAll", job.PID)
	}

	// Status+log files for this workdir must be gone: KillAll waits on the reaper
	// then removes them, so List sees nothing for this workdir.
	jobs, err := bgjobs.List(runsDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, j := range jobs {
		if j.Workdir == workDir {
			t.Fatalf("KillAll left job %s for workdir %s", j.ID, workDir)
		}
	}
}
