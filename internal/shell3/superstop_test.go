package shell3

import (
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/notify"
)

func TestNotifyTextNoWake(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("x"))
	s, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	s.NotifyTextNoWake("[superstop] everything was stopped")
	if !s.HasQueuedInput() {
		t.Fatal("notice not queued")
	}
	select {
	case ev := <-rt.Events():
		t.Fatalf("unexpected host event %+v (want no wake)", ev)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestKillAllForStopSuppressesCompletions(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("x"))
	host := &fakeHost{wakeOK: true}
	rt.SetCompletionHost(host)
	parent, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	id, err := rt.jobs.startCommand(parent, "sleep 30", t.TempDir(), []string{"sleep", "30"}, nil, notify.ReportAuto, "")
	if err != nil {
		t.Fatalf("startCommand: %v", err)
	}
	killed := parent.KillAllForStop()
	if len(killed) != 1 || killed[0].ID != id || killed[0].Kind != "command" {
		t.Fatalf("killed = %+v, want the one sleep job", killed)
	}
	if !strings.Contains(killed[0].Title, "sleep 30") {
		t.Errorf("killed title = %q, want the command text", killed[0].Title)
	}
	rt.jobs.wait()
	time.Sleep(50 * time.Millisecond)
	posts, wakes, fresh := host.snapshot()
	if len(posts)+len(wakes)+len(fresh) != 0 {
		t.Fatalf("suppressed kill still routed: posts=%v wakes=%v fresh=%v", posts, wakes, fresh)
	}
	if parent.HasQueuedInput() {
		t.Fatal("suppressed completion still queued a notice on the owner")
	}
	if again := parent.KillAllForStop(); len(again) != 0 {
		t.Fatalf("second KillAllForStop = %+v, want empty", again)
	}
}

func TestNormalKillStillRoutes(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("x"))
	host := &fakeHost{wakeOK: true}
	rt.SetCompletionHost(host)
	parent, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	id, err := rt.jobs.startCommand(parent, "sleep 30", t.TempDir(), []string{"sleep", "30"}, nil, notify.ReportAuto, "")
	if err != nil {
		t.Fatalf("startCommand: %v", err)
	}
	if err := rt.jobs.cancel(id, false); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	rt.jobs.wait()
	waitFor(t, "completion routed", func() bool {
		posts, wakes, fresh := host.snapshot()
		return len(posts)+len(wakes)+len(fresh) > 0
	})
}

func TestDispatchCompletionDropsSuppressed(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("x"))
	host := &fakeHost{wakeOK: true}
	rt.SetCompletionHost(host)
	rt.jobs.mu.Lock()
	rt.jobs.jobs["bg9"] = &bgJob{id: "bg9", kind: JobCommand, suppress: true}
	rt.jobs.mu.Unlock()
	ev := failedEvent()
	ev.JobID = "bg9"
	rt.jobs.dispatchCompletion(ev)
	time.Sleep(50 * time.Millisecond)
	posts, wakes, fresh := host.snapshot()
	if len(posts)+len(wakes)+len(fresh) != 0 {
		t.Fatalf("suppressed event routed: posts=%v wakes=%v fresh=%v", posts, wakes, fresh)
	}
}
