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
	if got := s.ID(); got != 0 {
		t.Errorf("default ID = %d, want 0", got)
	}
	s.id = 99
	if got := s.ID(); got != 99 {
		t.Errorf("ID() = %d, want 99", got)
	}
}
