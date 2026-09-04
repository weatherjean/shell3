package shell3

import (
	"context"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
)

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
	client := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "ok"}}})
	rt := newTestRuntime(t, func() chat.Config { return chat.Config{LLM: client} })
	s, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatal(err)
	}
	s.Interject("do the queued thing")

	terminal := false
	for ev := range s.RunQueued(context.Background()) {
		if ev.Kind == Done || ev.Kind == Error {
			terminal = true
		}
	}
	if !terminal {
		t.Fatal("RunQueued with queued items should run a turn (no terminal event)")
	}
	if !callsContain(client.CallsSnapshot(), "do the queued thing") {
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

func TestInterject_BusyQueuesSteering(t *testing.T) {
	rt := newTestRuntime(t, fakeCfg("ok"))
	s, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.busy = true
	s.mu.Unlock()

	s.Interject("steer mid-turn")
	if !s.HasQueuedSteer() {
		t.Fatal("busy Interject did not queue user steering")
	}

	s.mu.Lock()
	s.busy = false
	s.mu.Unlock()
}
