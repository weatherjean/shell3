//go:build unix

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
	runsDir := t.TempDir()
	dir := t.TempDir()
	marker := filepath.Join(dir, "marker")
	argv := []string{"bash", "-c", "echo ok > " + marker}
	job, err := Start(runsDir, argv, "display-cmd", dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	if job.Cmd != "display-cmd" {
		t.Fatalf("Cmd should be the display string, got %q", job.Cmd)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(marker); err == nil && len(b) > 0 {
			break // argv ran — wait for reaper before returning
		}
		time.Sleep(20 * time.Millisecond)
	}
	if b, _ := os.ReadFile(marker); len(b) == 0 {
		t.Fatal("argv background job did not run (marker never written)")
	}
	// Wait for the reaper goroutine to finish writing the status file before the
	// test returns and t.TempDir() cleanup fires.
	select {
	case <-job.Done():
	case <-time.After(5 * time.Second):
		t.Error("reaper goroutine did not finish within 5s")
	}
}
