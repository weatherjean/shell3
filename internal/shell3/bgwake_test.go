package shell3

import (
	"strings"
	"testing"
	"time"
)

// TestFailedCommandJobWakesParent verifies that a bash_bg job exiting nonzero
// wakes an idle parent session, so a hosted agent narrates the failure
// proactively instead of the notice sitting queued until the next user message.
func TestFailedCommandJobWakesParent(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("x"))
	parent, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	if _, err := rt.jobs.startCommand(parent, "false", t.TempDir(), []string{"false"}, nil, false); err != nil {
		t.Fatalf("startCommand: %v", err)
	}
	waitForWake(t, rt, parent)
	if !parent.HasQueuedInput() {
		t.Fatal("expected the failure notice queued in the parent inbox")
	}
}

// TestCleanCommandJobWakesParent verifies the default path: a bash_bg job
// exiting 0 wakes the parent with its completion notice, taking the same
// injectNotification path a failure does.
func TestCleanCommandJobWakesParent(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("x"))
	parent, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	if _, err := rt.jobs.startCommand(parent, "true", t.TempDir(), []string{"true"}, nil, false); err != nil {
		t.Fatalf("startCommand: %v", err)
	}
	waitForWake(t, rt, parent)
	if !parent.HasQueuedInput() {
		t.Fatal("expected the completion notice queued in the parent inbox")
	}
}

// TestQuietCommandJobStaysQuietOnCleanExit verifies a quiet:true bash_bg job
// queues a zero-exit completion notice without emitting a Wake.
func TestQuietCommandJobStaysQuietOnCleanExit(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("x"))
	parent, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	if _, err := rt.jobs.startCommand(parent, "true", t.TempDir(), []string{"true"}, nil, true); err != nil {
		t.Fatalf("startCommand: %v", err)
	}
	rt.jobs.wait() // job goroutine done → notice injected (or not)
	select {
	case ev := <-rt.Events():
		if ev.Kind == Wake && ev.Session == parent.ID() {
			t.Fatal("quiet clean exit must not wake the parent")
		}
	case <-time.After(200 * time.Millisecond):
	}
	if !parent.HasQueuedInput() {
		t.Fatal("expected the completion notice queued quietly")
	}
	// The queued notice should mention the job id and exit code 0.
	if got := rt.jobs.formatJobList(); !strings.Contains(got, "done") {
		t.Fatalf("job list = %q, want the finished job listed as done", got)
	}
}

// TestQuietCommandJobStillWakesOnFailure verifies quiet:true only silences
// clean exits — a nonzero exit wakes the parent regardless.
func TestQuietCommandJobStillWakesOnFailure(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("x"))
	parent, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	if _, err := rt.jobs.startCommand(parent, "false", t.TempDir(), []string{"false"}, nil, true); err != nil {
		t.Fatalf("startCommand: %v", err)
	}
	waitForWake(t, rt, parent)
	if !parent.HasQueuedInput() {
		t.Fatal("expected the failure notice queued in the parent inbox")
	}
}
