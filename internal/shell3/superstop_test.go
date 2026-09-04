package shell3

import (
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/inbox"
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
	parent, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	id, err := rt.jobs.startCommand(parent, "sleep 30", t.TempDir(), []string{"sleep", "30"}, nil)
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
	notices, total, err := rt.mainInbox().List("main", inbox.StatusPending, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 0 || len(notices) != 0 {
		t.Fatalf("suppressed kill persisted notices: total=%d notices=%+v", total, notices)
	}
	if again := parent.KillAllForStop(); len(again) != 0 {
		t.Fatalf("second KillAllForStop = %+v, want empty", again)
	}
}

func TestNormalKillPersistsFailureNotice(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("x"))
	parent, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	id, err := rt.jobs.startCommand(parent, "sleep 30", t.TempDir(), []string{"sleep", "30"}, nil)
	if err != nil {
		t.Fatalf("startCommand: %v", err)
	}
	if err := rt.jobs.cancel(id, false); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	rt.jobs.wait()
	notices, total, err := rt.mainInbox().List("main", inbox.StatusPending, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if total != 1 || len(notices) != 1 || notices[0].Message.Event != "bash_bg.failed" {
		t.Fatalf("failure notices: total=%d notices=%+v", total, notices)
	}
}
