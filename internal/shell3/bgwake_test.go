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
	if _, err := rt.jobs.startCommand(parent, "false", t.TempDir(), []string{"false"}, nil, false, ""); err != nil {
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
	if _, err := rt.jobs.startCommand(parent, "true", t.TempDir(), []string{"true"}, nil, false, ""); err != nil {
		t.Fatalf("startCommand: %v", err)
	}
	waitForWake(t, rt, parent)
	if !parent.HasQueuedInput() {
		t.Fatal("expected the completion notice queued in the parent inbox")
	}
}

// TestDirectCommandJobWakesParent verifies direct:true always wakes the owner
// with the notice — clean exit or not — bypassing the notifier entirely.
func TestDirectCommandJobWakesParent(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("x"))
	host := &fakeHost{}
	rt.SetCompletionHost(host) // must NOT be consulted for a direct job
	parent, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	if _, err := rt.jobs.startCommand(parent, "true", t.TempDir(), []string{"true"}, nil, true, ""); err != nil {
		t.Fatalf("startCommand: %v", err)
	}
	waitForWake(t, rt, parent)
	if !parent.HasQueuedInput() {
		t.Fatal("expected the completion notice queued in the parent inbox")
	}
	if posts, wakes, fresh := host.snapshot(); len(posts)+len(wakes)+len(fresh) != 0 {
		t.Fatalf("direct job must bypass the CompletionHost, got posts=%v wakes=%v fresh=%v", posts, wakes, fresh)
	}
}

// TestDefaultCommandJobPostsViaHost verifies the degraded-mode default: with a
// CompletionHost set and no notifier.md, a clean bash_bg completion posts raw
// through the host and the parent is not woken.
func TestDefaultCommandJobPostsViaHost(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("x"))
	host := &fakeHost{}
	rt.SetCompletionHost(host)
	parent, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	if _, err := rt.jobs.startCommand(parent, "true", t.TempDir(), []string{"true"}, nil, false, ""); err != nil {
		t.Fatalf("startCommand: %v", err)
	}
	rt.jobs.wait()
	waitFor(t, "raw post", func() bool { posts, _, _ := host.snapshot(); return len(posts) == 1 })
	select {
	case ev := <-rt.Events():
		if ev.Kind == Wake && ev.Session == parent.ID() {
			t.Fatal("default (triaged) completion must not wake the parent directly")
		}
	case <-time.After(200 * time.Millisecond):
	}
	if parent.HasQueuedInput() {
		t.Fatal("no notice should queue on the parent for a host-posted completion")
	}
	if got := rt.jobs.formatJobList(); !strings.Contains(got, "done") {
		t.Fatalf("job list = %q, want the finished job listed as done", got)
	}
}
