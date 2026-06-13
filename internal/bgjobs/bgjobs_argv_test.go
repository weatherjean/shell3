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
	job, err := Start(&fakeRegistry{}, argv, "display-cmd", dir, nil)
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
