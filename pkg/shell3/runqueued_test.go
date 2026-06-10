package shell3

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestInterject_IdleEmitsWake: Interject on an idle session emits a Wake for
// that session so the host knows to run a turn.
func TestInterject_IdleEmitsWake(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("ok"))
	s, err := rt.Session(SessionOpts{Name: "tg:1"})
	if err != nil {
		t.Fatal(err)
	}
	s.Interject("ping while idle")
	select {
	case ev := <-rt.Events():
		if ev.Kind != Wake || ev.Session != "tg:1" {
			t.Fatalf("want Wake for tg:1, got %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("idle Interject should emit Wake")
	}
}

// TestRunQueued_EmptyInboxNoTurn: RunQueued with an empty inbox starts no turn
// and returns an already-closed channel.
func TestRunQueued_EmptyInboxNoTurn(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("ok"))
	s, err := rt.Session(SessionOpts{Name: "tg:1"})
	if err != nil {
		t.Fatal(err)
	}
	for range s.RunQueued(context.Background()) {
	}
	if s.isBusy() {
		t.Fatal("RunQueued with empty inbox must not start a turn")
	}
}

// TestRunQueued_RunsTurnFromQueuedItems: RunQueued with queued items runs a turn
// that surfaces the queued text to the model (as the turn's reminder/seed) and
// drains the inbox, so a follow-up RunQueued is a no-op.
func TestRunQueued_RunsTurnFromQueuedItems(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("ok"))
	s, err := rt.Session(SessionOpts{Name: "tg:1"})
	if err != nil {
		t.Fatal(err)
	}
	// Drain the idle-Interject Wake so it doesn't confuse later assertions.
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

	// A follow-up RunQueued is a no-op: inbox is empty.
	for range s.RunQueued(context.Background()) {
	}
	if s.isBusy() {
		t.Fatal("second RunQueued must not start a turn (inbox drained)")
	}
}

// TestInterject_BusyDoesNotWake: an Interject during a running turn must NOT
// emit a Wake — the running turn drains the inbox itself.
func TestInterject_BusyDoesNotWake(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("ok"))
	s, err := rt.Session(SessionOpts{Name: "tg:1"})
	if err != nil {
		t.Fatal(err)
	}
	// Force the busy gate without a real turn by holding s.busy directly; this is
	// the focused branch test (isBusy() true => no wake).
	s.mu.Lock()
	s.busy = true
	s.mu.Unlock()

	s.Interject("steer mid-turn")
	select {
	case ev := <-rt.Events():
		t.Fatalf("busy Interject must not Wake, got %+v", ev)
	case <-time.After(200 * time.Millisecond):
		// no wake — correct
	}

	s.mu.Lock()
	s.busy = false
	s.mu.Unlock()
}
