package chat

import (
	"errors"
	"testing"
	"time"
)

func TestEmitAssistantTokenAndMessage(t *testing.T) {
	s := NewSession(SessionOpts{BufSize: 8})
	emitAssistantToken(s, "Hel")
	emitAssistantToken(s, "lo")
	emitAssistantMessage(s, "Hello")
	got := drainEvents(s, 3, 100*time.Millisecond)
	if len(got) != 3 {
		t.Fatalf("got %d events, want 3", len(got))
	}
	if got[0].Kind != EventAssistantToken || got[0].Text != "Hel" {
		t.Errorf("event[0]: %+v", got[0])
	}
	if got[1].Kind != EventAssistantToken || got[1].Text != "lo" {
		t.Errorf("event[1]: %+v", got[1])
	}
	if got[2].Kind != EventAssistantMessage || got[2].Text != "Hello" {
		t.Errorf("event[2]: %+v", got[2])
	}
}

func TestEmitUserMessage(t *testing.T) {
	s := NewSession(SessionOpts{BufSize: 2})
	emitUserMessage(s, "hi")
	got := drainEvents(s, 1, 50*time.Millisecond)
	if len(got) != 1 || got[0].Kind != EventUserMessage || got[0].Text != "hi" {
		t.Fatalf("user_message event mismatch: %+v", got)
	}
}

func TestEmitError(t *testing.T) {
	s := NewSession(SessionOpts{BufSize: 2})
	emitError(s, errors.New("boom"))
	got := drainEvents(s, 1, 50*time.Millisecond)
	if len(got) != 1 || got[0].Kind != EventError || got[0].Text != "boom" {
		t.Fatalf("error event mismatch: %+v", got)
	}
	if got[0].Err == nil || got[0].Err.Error() != "boom" {
		t.Fatalf("error event Err mismatch: %+v", got[0].Err)
	}
}

func TestEmitUsage(t *testing.T) {
	s := NewSession(SessionOpts{BufSize: 2})
	emitUsage(s, 100, 50, 150)
	got := drainEvents(s, 1, 50*time.Millisecond)
	if len(got) != 1 || got[0].Kind != EventUsage {
		t.Fatalf("usage event missing: %+v", got)
	}
	if got[0].Usage == nil || got[0].Usage.PromptTokens != 100 || got[0].Usage.CompletionTokens != 50 || got[0].Usage.TotalTokens != 150 {
		t.Errorf("usage data: %+v", got[0].Usage)
	}
}

func TestEmitAssistantReasoning(t *testing.T) {
	s := NewSession(SessionOpts{BufSize: 2})
	emitAssistantReasoning(s, "thinking...")
	got := drainEvents(s, 1, 50*time.Millisecond)
	if len(got) != 1 || got[0].Kind != EventAssistantReasoning || got[0].Text != "thinking..." {
		t.Fatalf("assistant_reasoning event mismatch: %+v", got)
	}
}

func TestEmitSystemReminder(t *testing.T) {
	s := NewSession(SessionOpts{BufSize: 2})
	emitSystemReminder(s, "ctx 50%")
	got := drainEvents(s, 1, 50*time.Millisecond)
	if len(got) != 1 || got[0].Kind != EventSystemReminder || got[0].Text != "ctx 50%" {
		t.Fatalf("system_reminder mismatch: %+v", got)
	}
}

func TestEmitTurnDone(t *testing.T) {
	s := NewSession(SessionOpts{BufSize: 2})
	emitTurnDone(s, 10, 20, 30)
	got := drainEvents(s, 1, 50*time.Millisecond)
	if len(got) != 1 || got[0].Kind != EventTurnDone {
		t.Fatalf("turn_done event missing: %+v", got)
	}
	if got[0].Usage == nil || got[0].Usage.TotalTokens != 30 {
		t.Errorf("usage data: %+v", got[0].Usage)
	}
}

func TestTerminalTurnDoneNotDroppedWhenBufferFull(t *testing.T) {
	s := NewSession(SessionOpts{BufSize: 2})
	// Fill the buffer to capacity. Nothing consumes from here until the
	// assertions below, so the buffer stays genuinely full while the terminal
	// emit is attempted — no consumer can race in and free a slot first.
	emitAssistantToken(s, "a")
	emitAssistantToken(s, "b")

	done := make(chan struct{})
	go func() {
		emitTurnDone(s, 1, 2, 3)
		close(done)
	}()

	// A guaranteed (blocking) send must NOT complete while the buffer is full —
	// it parks until a consumer frees a slot. The old lossy emit, by contrast,
	// returns immediately having silently dropped the terminal event, which is
	// exactly the hang-the-consumer bug. So a closed `done` here == dropped.
	select {
	case <-done:
		t.Fatal("emitTurnDone returned while the buffer was full — terminal event was dropped (lossy send)")
	case <-time.After(100 * time.Millisecond):
		// Good: the send is blocking, waiting for the consumer. 100ms is a
		// "did it block?" window, not a correctness timeout — a blocking send
		// never closes `done` here regardless of scheduling, so this can only
		// false-fail if a bare channel send is starved for 100ms (it isn't).
	}

	// Now drain. The terminal turn_done must be among the delivered events.
	sawTurnDone := false
	for range 3 {
		select {
		case ev := <-s.Events():
			if ev.Kind == EventTurnDone {
				sawTurnDone = true
			}
		case <-time.After(time.Second):
			t.Fatal("timed out draining events")
		}
	}
	if !sawTurnDone {
		t.Fatal("terminal turn_done never arrived")
	}
	<-done
}

func TestAssistantTokenStillDroppedWhenBufferFull(t *testing.T) {
	s := NewSession(SessionOpts{BufSize: 2})
	emitAssistantToken(s, "a")
	emitAssistantToken(s, "b")
	done := make(chan struct{})
	go func() {
		emitAssistantToken(s, "c") // must return immediately, not block
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("emitAssistantToken blocked on a full buffer; tokens must stay droppable")
	}
}
