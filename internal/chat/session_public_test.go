package chat

import "testing"

func TestSessionSinkReceivesEmit(t *testing.T) {
	s, c := newCollectorSession(SessionOpts{})
	emitAssistantToken(s, "hi")
	got := c.all()
	if len(got) != 1 || got[0].Kind != EventAssistantToken || got[0].Text != "hi" {
		t.Fatalf("unexpected events: %+v", got)
	}
}

func TestSessionIDAccessor(t *testing.T) {
	s := NewSession(SessionOpts{})
	if got := s.ID(); got != "" {
		t.Errorf("default ID = %q, want empty string", got)
	}
	s.id = "test-session-99"
	if got := s.ID(); got != "test-session-99" {
		t.Errorf("ID() = %q, want test-session-99", got)
	}
}
