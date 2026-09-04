package paths_test

import (
	"testing"

	"github.com/weatherjean/shell3/internal/paths"
)

func TestLocal(t *testing.T) {
	l := paths.NewLocal("/work/project")
	if l.Root != "/work/project/.shell3_project" {
		t.Fatalf("Root: got %q", l.Root)
	}
	if l.Runs != "/work/project/.shell3_project/runs" {
		t.Fatalf("Runs: got %q", l.Runs)
	}
	if l.Wrk != "/work/project/.shell3_project/wrk" {
		t.Fatalf("Wrk: got %q", l.Wrk)
	}
	if l.Errors != "/work/project/.shell3_project/errors.jsonl" {
		t.Fatalf("Errors: got %q", l.Errors)
	}
	if l.ScheduleLock != "/work/project/.shell3_project/schedule.lock" {
		t.Fatalf("ScheduleLock: got %q", l.ScheduleLock)
	}
}

func TestLastErrorPathIsSessionLocal(t *testing.T) {
	if got := paths.LastErrorPath("/work/project", "session-1"); got != "/work/project/.shell3_project/runs/session-1/last_error.json" {
		t.Fatalf("path = %q", got)
	}
	for _, sessionID := range []string{"unsafe/session", ".", ".."} {
		if got := paths.LastErrorPath("/work/project", sessionID); got != "/work/project/.shell3_project/last_error.json" {
			t.Fatalf("unsafe fallback for %q = %q", sessionID, got)
		}
	}
}
