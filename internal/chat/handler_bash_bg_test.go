package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/bgjobs"
)

// killAndReap SIGKILLs a job's process group, then waits for its reaper
// goroutine to finish all file I/O (Done closes after the reaper's final
// writeStatus). Without this wait, the reaper races t.TempDir's RemoveAll:
// it rewrites the .status file into jobs/ just as cleanup is deleting it,
// failing the rmdir with "directory not empty". Use in t.Cleanup so the wait
// runs before TempDir cleanup (Cleanup is LIFO; TempDir registers first).
func killAndReap(job bgjobs.Job) {
	_ = syscall.Kill(job.PID, syscall.SIGKILL)
	if d := job.Done(); d != nil {
		select {
		case <-d:
		case <-time.After(5 * time.Second):
		}
	}
}

func TestBashBgHandler_Name(t *testing.T) {
	h := BashBgHandler{}
	if h.Name() != "bash_bg" {
		t.Fatal("wrong name")
	}
}

func TestBashBgHandler_Execute_happyPath(t *testing.T) {
	wd := t.TempDir()
	runsDir := t.TempDir()
	h := BashBgHandler{}
	// Long-lived command so the recorded row survives ListJobs' dead-pid prune.
	// A fast command like `echo` can exit before the assertion runs, dropping
	// the row and making this flaky on loaded CI runners.
	args := json.RawMessage(`{"command":"sleep 30"}`)
	out, err := h.Execute(context.Background(), "1", args, ToolConfig{WorkDir: wd, RunsDir: runsDir})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "started bg_") {
		t.Fatalf("expected 'started bg_' prefix, got %q", out)
	}
	for _, want := range []string{"pid:", "log:", "tail -n", "kill ", ".shell3_project/runs/jobs"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in output: %q", want, out)
		}
	}
	// The job should now be recorded in the runs dir for this workdir.
	jobs, err := bgjobs.List(runsDir)
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("want 1 job recorded, got %d", len(jobs))
	}
	t.Cleanup(func() { killAndReap(jobs[0]) })
}

func TestBashBgHandler_Execute_requiresRunsDir(t *testing.T) {
	args := json.RawMessage(`{"command":"true"}`)
	_, err := BashBgHandler{}.Execute(context.Background(), "1", args, ToolConfig{WorkDir: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "require a runs directory") {
		t.Fatalf("expected runs-directory-required error, got %v", err)
	}
}

func TestBashBgHandler_Execute_badJSON(t *testing.T) {
	_, err := BashBgHandler{}.Execute(context.Background(), "1", json.RawMessage(`{not json`), ToolConfig{})
	if err == nil {
		t.Fatal("expected error on bad JSON")
	}
}

func TestBashBgHandler_Execute_emptyCommand(t *testing.T) {
	_, err := BashBgHandler{}.Execute(context.Background(), "1", json.RawMessage(`{"command":""}`), ToolConfig{WorkDir: t.TempDir()})
	if err == nil {
		t.Fatal("expected error on empty command")
	}
}

func TestBashBgHandler_Execute_workdirOverride(t *testing.T) {
	primary := t.TempDir()
	override := t.TempDir()
	runsDir := t.TempDir()
	// Use a long-lived command so the row survives bgjobs.List's dead-pid prune.
	args, _ := json.Marshal(map[string]string{"command": "sleep 30", "workdir": override})
	out, err := BashBgHandler{}.Execute(context.Background(), "1", args, ToolConfig{WorkDir: primary, RunsDir: runsDir})
	if err != nil {
		t.Fatal(err)
	}
	// The job should be recorded (all jobs share the same runsDir).
	jobs, err := bgjobs.List(runsDir)
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("want 1 job in runsDir, got %d", len(jobs))
	}
	// Verify the override workdir is recorded in the job.
	if jobs[0].Workdir != override {
		t.Fatalf("job workdir = %q, want %q", jobs[0].Workdir, override)
	}
	t.Cleanup(func() { killAndReap(jobs[0]) })
	if !strings.Contains(out, ".shell3_project/runs/jobs") {
		t.Fatalf("output should reference '.shell3_project/runs/jobs'; got %q", out)
	}
	_ = primary // used in ToolConfig above
}

// End-to-end: spawn, verify alive via kill -0, then stop via kill.
func TestBashBgHandler_Execute_processControllableByModel(t *testing.T) {
	wd := t.TempDir()
	runsDir := t.TempDir()
	args := json.RawMessage(`{"command":"sleep 30"}`)
	out, err := BashBgHandler{}.Execute(context.Background(), "1", args, ToolConfig{WorkDir: wd, RunsDir: runsDir})
	if err != nil {
		t.Fatal(err)
	}
	// Pull pid from output.
	var pid int
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "pid:") {
			if _, err := json.Number(strings.TrimSpace(strings.TrimPrefix(line, "pid:"))).Int64(); err == nil {
				_, _ = fmtSscan(line, &pid)
			}
		}
	}
	if pid == 0 {
		t.Fatalf("could not parse pid from output: %q", out)
	}
	if err := syscall.Kill(pid, 0); err != nil {
		t.Fatalf("process not alive: %v", err)
	}
	// Recover the Job (with its reaper done-channel) so cleanup waits for the
	// reaper before t.TempDir's RemoveAll — see killAndReap.
	jobs, err := bgjobs.List(runsDir)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("want 1 job recorded, got %d (err=%v)", len(jobs), err)
	}
	t.Cleanup(func() { killAndReap(jobs[0]) })
}

// fmtSscan is a tiny indirection to avoid importing fmt in the production
// file just for tests — the test uses Sscanf for parsing.
func fmtSscan(line string, pid *int) (int, error) {
	return fmt.Sscanf(line, "pid: %d", pid)
}
