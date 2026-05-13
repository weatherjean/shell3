package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestBashBgHandler_Name(t *testing.T) {
	h := BashBgHandler{}
	if h.Name() != "bash_bg" {
		t.Fatal("wrong name")
	}
}

func TestBashBgHandler_Execute_happyPath(t *testing.T) {
	wd := t.TempDir()
	h := BashBgHandler{}
	args := json.RawMessage(`{"command":"echo bg-output"}`)
	out, err := h.Execute(context.Background(), "1", args, ToolConfig{WorkDir: wd})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "started bg_") {
		t.Fatalf("expected 'started bg_' prefix, got %q", out)
	}
	for _, want := range []string{"pid:", "log:", "tail -n", "kill ", "cat "} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in output: %q", want, out)
		}
	}
	// bg.json should now exist in workdir.
	if _, err := os.Stat(filepath.Join(wd, ".shell3", "bg.json")); err != nil {
		t.Fatalf("bg.json not written: %v", err)
	}
	// Give the spawned echo a moment to exit + log to flush, then clean up.
	time.Sleep(200 * time.Millisecond)
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
	args, _ := json.Marshal(map[string]string{"command": "true", "workdir": override})
	out, err := BashBgHandler{}.Execute(context.Background(), "1", args, ToolConfig{WorkDir: primary})
	if err != nil {
		t.Fatal(err)
	}
	// bg.json should land in override, not primary.
	if _, err := os.Stat(filepath.Join(override, ".shell3", "bg.json")); err != nil {
		t.Fatalf("bg.json missing in override: %v", err)
	}
	if _, err := os.Stat(filepath.Join(primary, ".shell3", "bg.json")); err == nil {
		t.Fatalf("bg.json should NOT exist in primary")
	}
	if !strings.Contains(out, override) {
		t.Fatalf("output should reference override path; got %q", out)
	}
}

// End-to-end: spawn, verify alive via kill -0, then stop via kill.
func TestBashBgHandler_Execute_processControllableByModel(t *testing.T) {
	wd := t.TempDir()
	args := json.RawMessage(`{"command":"sleep 30"}`)
	out, err := BashBgHandler{}.Execute(context.Background(), "1", args, ToolConfig{WorkDir: wd})
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
