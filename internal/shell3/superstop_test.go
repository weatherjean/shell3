package shell3

import (
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/notify"
)

// NotifyTextNoWake queues a notice for the next turn without waking the
// session — /superstop's summary must not spend a turn announcing itself.
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

// KillAllForStop kills every live job and suppresses their completion
// routing: no ⚠️ floor post, no owner wake — the superstop summary the caller
// builds from the returned list is the single record.
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
	time.Sleep(50 * time.Millisecond) // let any (wrong) delivery land
	posts, wakes, fresh := host.snapshot()
	if len(posts)+len(wakes)+len(fresh) != 0 {
		t.Fatalf("suppressed kill still routed: posts=%v wakes=%v fresh=%v", posts, wakes, fresh)
	}
	if parent.HasQueuedInput() {
		t.Fatal("suppressed completion still queued a notice on the owner")
	}
	// Idempotent: nothing left to kill.
	if again := parent.KillAllForStop(); len(again) != 0 {
		t.Fatalf("second KillAllForStop = %+v, want empty", again)
	}
}

// A normal single-job kill still routes its completion — suppression is
// superstop's, not cancellation's.
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
	if err := parent.KillJob(id); err != nil {
		t.Fatalf("KillJob: %v", err)
	}
	rt.jobs.wait()
	waitFor(t, "completion routed", func() bool {
		posts, wakes, fresh := host.snapshot()
		return len(posts)+len(wakes)+len(fresh) > 0
	})
}

// A lingering subagent — main turn finished, child still open for its
// bash_bg jobs — must be poisoned by superstop even though it is `finished`:
// otherwise its child keeps running follow-up turns that route normally after
// "everything stopped".
func TestKillAllForStopPoisonsLingeringSubagent(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("x"))
	child, err := rt.Session(SessionOpts{Name: "child", Headless: true})
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	parent, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	rt.jobs.mu.Lock()
	rt.jobs.jobs["sub1"] = &bgJob{
		id: "sub1", kind: JobSubagent, title: "lingering worker",
		finished: true, lingering: true, child: child, childClosed: false,
	}
	rt.jobs.mu.Unlock()

	parent.KillAllForStop()

	rt.jobs.mu.Lock()
	j := rt.jobs.jobs["sub1"]
	suppressed, noFollow := j.suppress, j.noFollowUps
	rt.jobs.mu.Unlock()
	if !suppressed || !noFollow {
		t.Fatalf("lingering subagent not poisoned: suppress=%v noFollowUps=%v", suppressed, noFollow)
	}
}

// Suppression is keyed by job id at dispatch time, so it covers every kind —
// including a subagent's own completion event.
func TestDispatchCompletionDropsSuppressed(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("x"))
	host := &fakeHost{wakeOK: true}
	rt.SetCompletionHost(host)
	rt.jobs.mu.Lock()
	rt.jobs.jobs["sub9"] = &bgJob{id: "sub9", kind: JobSubagent, suppress: true}
	rt.jobs.mu.Unlock()
	ev := failedEvent()
	ev.JobID = "sub9"
	rt.jobs.dispatchCompletion(ev)
	time.Sleep(50 * time.Millisecond)
	posts, wakes, fresh := host.snapshot()
	if len(posts)+len(wakes)+len(fresh) != 0 {
		t.Fatalf("suppressed event routed: posts=%v wakes=%v fresh=%v", posts, wakes, fresh)
	}
}
