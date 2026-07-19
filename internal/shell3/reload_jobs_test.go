package shell3

import (
	"strings"
	"testing"
)

// TestReloadRefusesWhileJobsRunning verifies Reload rejects immediately —
// before building the new config — when any background job is live, naming
// the running task ids in the error.
func TestReloadRefusesWhileJobsRunning(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("x"))
	parent, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	id, err := rt.jobs.startCommand(parent, "sleep", t.TempDir(), []string{"sleep", "30"}, nil, false)
	if err != nil {
		t.Fatalf("startCommand: %v", err)
	}
	_, err = rt.Reload()
	if err == nil {
		t.Fatal("Reload succeeded with a running background job, want refusal")
	}
	if !strings.Contains(err.Error(), "background task(s) running") || !strings.Contains(err.Error(), id) {
		t.Fatalf("Reload error = %q, want a running-jobs refusal naming %s", err, id)
	}
	if err := rt.jobs.cancel(id); err != nil {
		t.Fatalf("cancel: %v", err)
	}
}
