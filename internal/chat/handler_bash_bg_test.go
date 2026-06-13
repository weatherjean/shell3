package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/store"
)

// memStore opens a fresh in-memory store for a bash_bg test.
func memStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestBashBgHandler_Name(t *testing.T) {
	h := BashBgHandler{}
	if h.Name() != "bash_bg" {
		t.Fatal("wrong name")
	}
}

func TestBashBgHandler_Execute_happyPath(t *testing.T) {
	wd := t.TempDir()
	h := BashBgHandler{}
	st := memStore(t)
	args := json.RawMessage(`{"command":"echo bg-output"}`)
	out, err := h.Execute(context.Background(), "1", args, ToolConfig{WorkDir: wd, Store: st})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "started bg_") {
		t.Fatalf("expected 'started bg_' prefix, got %q", out)
	}
	for _, want := range []string{"pid:", "log:", "tail -n", "kill ", "shell3 jobs"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in output: %q", want, out)
		}
	}
	// The job should now be recorded in the store for this workdir.
	jobs, err := st.ListJobs(wd, 0, 0)
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("want 1 job recorded, got %d", len(jobs))
	}
	// Give the spawned echo a moment to exit + log to flush, then clean up.
	time.Sleep(200 * time.Millisecond)
}

func TestBashBgHandler_Execute_requiresStore(t *testing.T) {
	args := json.RawMessage(`{"command":"true"}`)
	_, err := BashBgHandler{}.Execute(context.Background(), "1", args, ToolConfig{WorkDir: t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "require a store") {
		t.Fatalf("expected store-required error, got %v", err)
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
	st := memStore(t)
	// Use a long-lived command so the row survives ListJobs' dead-pid prune.
	args, _ := json.Marshal(map[string]string{"command": "sleep 30", "workdir": override})
	out, err := BashBgHandler{}.Execute(context.Background(), "1", args, ToolConfig{WorkDir: primary, Store: st})
	if err != nil {
		t.Fatal(err)
	}
	// The job should be recorded under override, not primary.
	overJobs, err := st.ListJobs(override, 0, 0)
	if err != nil {
		t.Fatalf("list override: %v", err)
	}
	if len(overJobs) != 1 {
		t.Fatalf("want 1 job under override, got %d", len(overJobs))
	}
	t.Cleanup(func() { syscall.Kill(overJobs[0].PID, syscall.SIGKILL) })
	if priJobs, _ := st.ListJobs(primary, 0, 0); len(priJobs) != 0 {
		t.Fatalf("primary should have no jobs, got %d", len(priJobs))
	}
	if !strings.Contains(out, "shell3 jobs") {
		t.Fatalf("output should reference 'shell3 jobs'; got %q", out)
	}
}

// End-to-end: spawn, verify alive via kill -0, then stop via kill.
func TestBashBgHandler_Execute_processControllableByModel(t *testing.T) {
	wd := t.TempDir()
	args := json.RawMessage(`{"command":"sleep 30"}`)
	out, err := BashBgHandler{}.Execute(context.Background(), "1", args, ToolConfig{WorkDir: wd, Store: memStore(t)})
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
	t.Cleanup(func() { syscall.Kill(pid, syscall.SIGKILL) })
}

// fmtSscan is a tiny indirection to avoid importing fmt in the production
// file just for tests — the test uses Sscanf for parsing.
func fmtSscan(line string, pid *int) (int, error) {
	return fmt.Sscanf(line, "pid: %d", pid)
}
