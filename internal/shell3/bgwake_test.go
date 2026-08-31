package shell3

import (
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/notify"
)

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
