package chat

import (
	"errors"
	"testing"
)

func TestEmitAssistantTokenAndMessage(t *testing.T) {
	s, c := newCollectorSession(SessionOpts{})
	emitAssistantToken(s, "Hel")
	emitAssistantToken(s, "lo")
	emitAssistantMessage(s, "Hello")
	got := c.all()
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
	s, c := newCollectorSession(SessionOpts{})
	emitUserMessage(s, "hi")
	got := c.all()
	if len(got) != 1 || got[0].Kind != EventUserMessage || got[0].Text != "hi" {
		t.Fatalf("user_message event mismatch: %+v", got)
	}
}

func TestEmitError(t *testing.T) {
	s, c := newCollectorSession(SessionOpts{})
	emitError(s, errors.New("boom"))
	got := c.all()
	if len(got) != 1 || got[0].Kind != EventError || got[0].Text != "boom" {
		t.Fatalf("error event mismatch: %+v", got)
	}
	if got[0].Err == nil || got[0].Err.Error() != "boom" {
		t.Fatalf("error event Err mismatch: %+v", got[0].Err)
	}
}

func TestEmitUsage(t *testing.T) {
	s, c := newCollectorSession(SessionOpts{})
	emitUsage(s, 100, 50, 150)
	got := c.all()
	if len(got) != 1 || got[0].Kind != EventUsage {
		t.Fatalf("usage event missing: %+v", got)
	}
	if got[0].Usage == nil || got[0].Usage.PromptTokens != 100 || got[0].Usage.CompletionTokens != 50 || got[0].Usage.TotalTokens != 150 {
		t.Errorf("usage data: %+v", got[0].Usage)
	}
}

func TestEmitAssistantReasoning(t *testing.T) {
	s, c := newCollectorSession(SessionOpts{})
	emitAssistantReasoning(s, "thinking...")
	got := c.all()
	if len(got) != 1 || got[0].Kind != EventAssistantReasoning || got[0].Text != "thinking..." {
		t.Fatalf("assistant_reasoning event mismatch: %+v", got)
	}
}

func TestEmitSystemReminder(t *testing.T) {
	s, c := newCollectorSession(SessionOpts{})
	emitSystemReminder(s, "ctx 50%")
	got := c.all()
	if len(got) != 1 || got[0].Kind != EventSystemReminder || got[0].Text != "ctx 50%" {
		t.Fatalf("system_reminder mismatch: %+v", got)
	}
}

func TestEmitTurnDone(t *testing.T) {
	s, c := newCollectorSession(SessionOpts{})
	emitTurnDone(s, 10, 20, 30)
	got := c.all()
	if len(got) != 1 || got[0].Kind != EventTurnDone {
		t.Fatalf("turn_done event missing: %+v", got)
	}
	if got[0].Usage == nil || got[0].Usage.TotalTokens != 30 {
		t.Errorf("usage data: %+v", got[0].Usage)
	}
}

// TestSinkDeliversEveryEventInOrder pins the sink-mode guarantee that replaced
// the old buffered channel: every emit — high-volume tokens included — is
// delivered synchronously and in order, with nothing dropped. (The old design
// had a non-blocking emit that silently dropped tokens when the buffer filled;
// that path no longer exists.)
func TestSinkDeliversEveryEventInOrder(t *testing.T) {
	s, c := newCollectorSession(SessionOpts{})
	const n = 1000
	for range n {
		emitAssistantToken(s, "x")
	}
	emitTurnDone(s, 1, 2, 3)
	got := c.all()
	if len(got) != n+1 {
		t.Fatalf("delivered %d events, want %d (no drops)", len(got), n+1)
	}
	if got[n].Kind != EventTurnDone {
		t.Errorf("last event = %v, want EventTurnDone", got[n].Kind)
	}
}
