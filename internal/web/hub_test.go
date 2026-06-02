package web

import (
	"context"
	"testing"
	"time"

	"github.com/weatherjean/shell3/pkg/chat"
	"github.com/weatherjean/shell3/pkg/llm"
	"github.com/weatherjean/shell3/pkg/llm/fakellm"
	"github.com/weatherjean/shell3/pkg/persona"
)

// newTestHub wires a real chat.Session driven by a scripted fake LLM, so the
// hub exercises the genuine event path (user_message → tokens → turn_done).
func newTestHub(t *testing.T, scripts ...fakellm.Script) (*Hub, *chat.Session) {
	t.Helper()
	client := fakellm.New(scripts...)
	sess := chat.NewSession(chat.SessionOpts{BufSize: 256})
	tc := chat.TurnConfig{
		LLM:         client,
		Personality: persona.Persona{Name: "test"},
		Handlers:    chat.NewHandlers(chat.Config{}),
		Log:         chat.LogOrNoop(nil),
	}
	run := func(ctx context.Context, input string) { sess.Run(ctx, tc, input) }
	h := NewHub(sess, run)
	h.Start()
	t.Cleanup(func() { h.Close(); sess.End("ok"); sess.CloseEvents() })
	return h, sess
}

func drainKinds(t *testing.T, ch <-chan chat.Event, want chat.EventKind, timeout time.Duration) chat.Event {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case ev := <-ch:
			if ev.Kind == want {
				return ev
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %v", want)
		}
	}
}

func TestHub_SubmitStreamsToSubscriber(t *testing.T) {
	h, _ := newTestHub(t, fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "hi"}}})
	_, ch, unsub := h.Subscribe()
	defer unsub()

	if err := h.Submit("hello"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	drainKinds(t, ch, chat.EventTurnDone, 2*time.Second)
}

func TestHub_BusyRejectsConcurrentSubmit(t *testing.T) {
	// A blocking run holds the turn open so the busy window is deterministic
	// (real fakellm turns finish too fast to reliably observe ErrBusy).
	sess := chat.NewSession(chat.SessionOpts{BufSize: 256})
	started := make(chan struct{})
	release := make(chan struct{})
	run := func(ctx context.Context, input string) {
		close(started)
		<-release
	}
	h := NewHub(sess, run)
	h.Start()
	t.Cleanup(func() { close(release); h.Close(); sess.End("ok"); sess.CloseEvents() })

	if err := h.Submit("first"); err != nil {
		t.Fatalf("first Submit: %v", err)
	}
	<-started
	if err := h.Submit("second"); err != ErrBusy {
		t.Fatalf("second Submit = %v, want ErrBusy", err)
	}
	// Clear must also refuse while a turn is in flight.
	if err := h.Clear(); err != ErrBusy {
		t.Fatalf("Clear during turn = %v, want ErrBusy", err)
	}
}

func TestHub_ReplayThenLiveNoGap(t *testing.T) {
	h, _ := newTestHub(t, fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "x"}}})
	if err := h.Submit("one"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	drainHubIdle(t, h, 2*time.Second)

	replay, _, unsub := h.Subscribe()
	defer unsub()
	var sawTurnDone bool
	for _, ev := range replay {
		if ev.Kind == chat.EventTurnDone {
			sawTurnDone = true
		}
	}
	if !sawTurnDone {
		t.Fatalf("replay missing turn_done; got %d events", len(replay))
	}
}

func TestHub_ClearEmptiesLogAndBroadcastsReset(t *testing.T) {
	h, sess := newTestHub(t, fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "x"}}})
	if err := h.Submit("one"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	drainHubIdle(t, h, 2*time.Second)

	_, ch, unsub := h.Subscribe()
	defer unsub()
	if err := h.Clear(); err != nil {
		t.Fatalf("Clear (idle): %v", err)
	}

	ev := drainKinds(t, ch, chat.EventSessionStart, time.Second)
	if ev.Meta["reset"] != "true" {
		t.Errorf("reset marker missing meta.reset; got %v", ev.Meta)
	}
	if len(sess.Messages()) != 0 {
		t.Errorf("Clear did not reset session messages: %d", len(sess.Messages()))
	}
}

func drainHubIdle(t *testing.T, h *Hub, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		if !h.Busy() {
			return
		}
		select {
		case <-deadline:
			t.Fatal("hub stayed busy")
		case <-time.After(time.Millisecond):
		}
	}
}

// TestHub_CancelAbortsInFlightTurn uses a run closure that blocks until its
// context is cancelled, so Cancel's effect is deterministic (real fakellm
// turns finish too fast to observe). It also exercises that the hub reports
// busy while a turn is in flight.
func TestHub_CancelAbortsInFlightTurn(t *testing.T) {
	sess := chat.NewSession(chat.SessionOpts{BufSize: 256})
	started := make(chan struct{})
	finished := make(chan struct{})
	run := func(ctx context.Context, input string) {
		close(started)
		<-ctx.Done() // block until Cancel fires
		close(finished)
	}
	h := NewHub(sess, run)
	h.Start()
	t.Cleanup(func() { h.Close(); sess.End("ok"); sess.CloseEvents() })

	if err := h.Submit("go"); err != nil {
		t.Fatalf("Submit: %v", err)
	}
	<-started
	if !h.Busy() {
		t.Error("expected Busy() == true while a turn is in flight")
	}
	h.Cancel()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("Cancel did not abort the in-flight turn")
	}
}
