package bgjobs

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestStartExecsArgv proves Start execs the given argv (not a literal bash -c
// command) and records the display string as Cmd.
func TestStartExecsArgv(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "marker")
	argv := []string{"bash", "-c", "echo ok > " + marker}
	job, err := Start(argv, "display-cmd", dir, nil, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if job.Cmd != "display-cmd" {
		t.Fatalf("Cmd should be the display string, got %q", job.Cmd)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(marker); err == nil && len(b) > 0 {
			return // argv ran
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("argv background job did not run (marker never written)")
}

// TestStartSinkRecordsDisplayNotArgv proves the bg_done notification's Cmd field
// carries the human-readable display string — the original model command — and
// NOT argv[0] ("bash") or the argv-joined form, even after a runner swap where
// argv differs from display. Uses notifyOnExit=true + a real sink so the reaper
// actually writes the notification (TestStartExecsArgv passes notifyOnExit=false
// and so never exercises the sink path).
func TestStartSinkRecordsDisplayNotArgv(t *testing.T) {
	wd := t.TempDir()
	sinkPath := filepath.Join(wd, ".shell3", "sink", "main.jsonl")
	const display = "the original model command"
	argv := []string{"bash", "-c", "true"} // runner-swap flavor: argv != display
	job, err := Start(argv, display, wd, nil, sinkPath, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Remove(job.Log) })

	lines := waitSinkLines(t, sinkPath, 1, 3*time.Second)
	n := lines[0]
	if n["kind"] != "bg_done" {
		t.Fatalf("kind = %v, want bg_done", n["kind"])
	}
	if n["cmd"] != display {
		t.Fatalf("cmd = %v, want display %q (must not be argv[0] or argv-joined)", n["cmd"], display)
	}
}
