package chat

import (
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
	emitError(s, "boom")
	got := drainEvents(s, 1, 50*time.Millisecond)
	if len(got) != 1 || got[0].Kind != EventError || got[0].Text != "boom" {
		t.Fatalf("error event mismatch: %+v", got)
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
