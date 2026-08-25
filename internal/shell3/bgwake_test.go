package shell3

import (
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/notify"
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
	if _, err := rt.jobs.startCommand(parent, "false", t.TempDir(), []string{"false"}, nil, notify.ReportAuto, ""); err != nil {
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
	if _, err := rt.jobs.startCommand(parent, "true", t.TempDir(), []string{"true"}, nil, notify.ReportAuto, ""); err != nil {
		t.Fatalf("startCommand: %v", err)
	}
	waitForWake(t, rt, parent)
	if !parent.HasQueuedInput() {
		t.Fatal("expected the completion notice queued in the parent inbox")
	}
}

// TestDirectCommandJobPostsRaw verifies direct:true posts the raw result to
// the user and queues the notice on the owner WITHOUT waking it — the user is
// already served; the agent sees it next turn.
func TestDirectCommandJobPostsRaw(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("x"))
	host := &fakeHost{wakeOK: true}
	rt.SetCompletionHost(host)
	parent, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	if _, err := rt.jobs.startCommand(parent, "true", t.TempDir(), []string{"true"}, nil, notify.ReportRaw, ""); err != nil {
		t.Fatalf("startCommand: %v", err)
	}
	rt.jobs.wait()
	waitFor(t, "raw post", func() bool { posts, _, _ := host.snapshot(); return len(posts) == 1 })
	if _, wakes, fresh := host.snapshot(); len(wakes)+len(fresh) != 0 {
		t.Fatalf("direct job must not run an agent turn, got wakes=%v fresh=%v", wakes, fresh)
	}
	if !parent.HasQueuedInput() {
		t.Fatal("expected the notice queued (no wake) in the parent inbox")
	}
}

// TestDefaultCommandJobMailsOwner verifies the default with a host installed:
// a clean bash_bg completion is mail to the agent — WakeOwner carries it, and
// nothing posts to the user.
func TestDefaultCommandJobMailsOwner(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("x"))
	host := &fakeHost{wakeOK: true}
	rt.SetCompletionHost(host)
	parent, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	if _, err := rt.jobs.startCommand(parent, "true", t.TempDir(), []string{"true"}, nil, notify.ReportAuto, ""); err != nil {
		t.Fatalf("startCommand: %v", err)
	}
	rt.jobs.wait()
	waitFor(t, "owner mail", func() bool { _, wakes, _ := host.snapshot(); return len(wakes) == 1 })
	if posts, _, fresh := host.snapshot(); len(posts)+len(fresh) != 0 {
		t.Fatalf("default completion must not post or start fresh turns, got posts=%v fresh=%v", posts, fresh)
	}
	if got := rt.jobs.formatJobList(); !strings.Contains(got, "done") {
		t.Fatalf("job list = %q, want the finished job listed as done", got)
	}
}
