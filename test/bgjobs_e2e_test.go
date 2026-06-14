//go:build unix

package test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/bgjobs"
	"github.com/weatherjean/shell3/internal/jobstore"
	"github.com/weatherjean/shell3/internal/proc"
	"github.com/weatherjean/shell3/internal/store"
)

// TestBgJobs_LifecycleAndPrune proves the full store-backed bg-job lifecycle
// against the real CLI binary: a real tracked background process appears in
// `shell3 jobs`, and once its process is killed the next `shell3 jobs` prunes
// it (so it stops being listed) and the underlying table row is physically
// gone from the store.
func TestBgJobs_LifecycleAndPrune(t *testing.T) {
	homeDir := t.TempDir()
	workDir, err := os.MkdirTemp("/tmp", "bgjob")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(workDir) })

	dbPath := filepath.Join(homeDir, ".shell3", "data", "shell3.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	reg := jobstore.New(st)

	// Build the CLI BEFORE spawning the job: compiling cmd/shell3 (which pulls
	// in the pure-Go modernc.org/sqlite) can take tens of seconds on a cold
	// runner, and the spawned process must outlive that compile. Building first
	// keeps the slow step out of the job's lifetime window.
	bin := buildShell3(t)

	// Spawn a REAL long-lived bg process, tracked via the store registry. Use a
	// generous sleep so the job stays live across the (already-built) CLI calls
	// even on a slow/loaded CI runner.
	job, err := bgjobs.Start(reg, []string{"sleep", "120"}, "sleep 120", workDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Ensure cleanup kills the group even if asserts fail.
	t.Cleanup(func() { _ = syscall.Kill(-job.PID, syscall.SIGKILL) })

	// runJobs invokes the built binary's `jobs` subcommand against the same
	// canonical DB (resolved from HOME) and returns its stdout.
	runJobs := func() string {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, bin, "jobs", "--workdir", workDir)
		cmd.Dir = workDir
		cmd.Env = append(os.Environ(), "HOME="+homeDir)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("shell3 jobs failed: %v\n%s", err, out)
		}
		return string(out)
	}

	// 1) The live job is listed.
	if out := runJobs(); !strings.Contains(out, job.ID) {
		t.Fatalf("shell3 jobs did not list live job %s:\n%s", job.ID, out)
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

	// 3) Next `shell3 jobs` must PRUNE it (not listed). Poll because
	//    reaping/visibility can lag slightly.
	deadline = time.Now().Add(10 * time.Second)
	pruned := false
	for time.Now().Before(deadline) {
		if !strings.Contains(runJobs(), job.ID) {
			pruned = true
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !pruned {
		t.Fatalf("dead job %s was not pruned from shell3 jobs", job.ID)
	}

	// Confirm the table row is physically gone via the store (ListJobs prunes).
	rows, err := st.ListJobs(workDir, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.ID == job.ID {
			t.Fatalf("dead job row %s still in table", job.ID)
		}
	}
}

// TestBgJobs_KillAllOverStore proves bgjobs.KillAll, operating over the
// store-backed registry, kills the tracked OS process(es) for a workdir and
// clears their rows from the jobs table.
func TestBgJobs_KillAllOverStore(t *testing.T) {
	homeDir := t.TempDir()
	workDir, err := os.MkdirTemp("/tmp", "bgkill")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(workDir) })

	dbPath := filepath.Join(homeDir, ".shell3", "data", "shell3.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	reg := jobstore.New(st)

	job, err := bgjobs.Start(reg, []string{"sleep", "30"}, "sleep 30", workDir, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(-job.PID, syscall.SIGKILL) })
	if !proc.Alive(job.PID) {
		t.Fatal("spawned job not alive")
	}

	killed, err := bgjobs.KillAll(reg, workDir)
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

	// rows cleared: read the jobs table via a RAW read-only query that does
	// NOT prune dead pids, so this genuinely proves KillAll's Clear step
	// deleted the rows (ListJobs would zero out via pruning regardless).
	db := roDB(t, dbPath)
	defer db.Close()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM jobs WHERE workdir = ?`, workDir).Scan(&n); err != nil {
		t.Fatalf("count jobs: %v", err)
	}
	if n != 0 {
		t.Fatalf("KillAll left %d job rows for workdir, want 0 (Clear did not delete)", n)
	}
}
