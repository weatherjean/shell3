package shell3

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestInterject_IdleEmitsWake(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("ok"))
	s, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatal(err)
	}
	s.Interject("ping while idle")
	select {
	case ev := <-rt.Events():
		if ev.Kind != Wake || ev.Session != s.ID() {
			t.Fatalf("want Wake for %s, got %+v", s.ID(), ev)
		}
	case <-time.After(time.Second):
		t.Fatal("idle Interject should emit Wake")
	}
}

func TestRunQueued_EmptyInboxNoTurn(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("ok"))
	s, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatal(err)
	}
	for range s.RunQueued(context.Background()) {
	}
	if s.isBusy() {
		t.Fatal("RunQueued with empty inbox must not start a turn")
	}
}

func TestRunQueued_RunsTurnFromQueuedItems(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("ok"))
	s, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatal(err)
	}
	s.Interject("do the queued thing")
	select {
	case <-rt.Events():
	case <-time.After(time.Second):
		t.Fatal("expected Wake after idle Interject")
	}

	sawReminder := false
	terminal := false
	for ev := range s.RunQueued(context.Background()) {
		if ev.Kind == SystemReminder && strings.Contains(ev.Text, "do the queued thing") {
			sawReminder = true
		}
		if ev.Kind == Done || ev.Kind == Error {
			terminal = true
		}
	}
	if !terminal {
		t.Fatal("RunQueued with queued items should run a turn (no terminal event)")
	}
	if !sawReminder {
		t.Fatal("queued text not surfaced to the model as a reminder")
	}
	if s.sess.HasInbox() {
		t.Fatal("inbox should be drained after RunQueued ran a turn")
	}

	for range s.RunQueued(context.Background()) {
	}
	if s.isBusy() {
		t.Fatal("second RunQueued must not start a turn (inbox drained)")
	}
}

func TestRunQueued_BusyReturnsClosedChannelNoTurn(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("ok"))
	s, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatal(err)
	}
	s.sess.Interject("queued while busy")
	s.mu.Lock()
	s.busy = true
	s.mu.Unlock()

	ch := s.RunQueued(context.Background())
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("busy RunQueued must return an already-closed channel (no events)")
		}
	case <-time.After(time.Second):
		t.Fatal("busy RunQueued channel should be already closed, not blocking")
	}

	s.mu.Lock()
	stillBusyFromGate := s.busy
	s.busy = false
	s.mu.Unlock()
	if !stillBusyFromGate {
		t.Fatal("busy gate flipped unexpectedly — RunQueued may have started a turn")
	}
	if !s.sess.HasInbox() {
		t.Fatal("busy RunQueued must not drain the inbox")
	}
}

func TestInterject_BusyDoesNotWake(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("ok"))
	s, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.busy = true
	s.mu.Unlock()

	s.Interject("steer mid-turn")
	select {
	case ev := <-rt.Events():
		t.Fatalf("busy Interject must not Wake, got %+v", ev)
	case <-time.After(200 * time.Millisecond):
	}

	s.mu.Lock()
	s.busy = false
	s.mu.Unlock()
}
