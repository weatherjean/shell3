package chat

import (
	"testing"
	"time"
)

func TestSessionEventsAccessor(t *testing.T) {
	s := NewSession(SessionOpts{BufSize: 4})
	ch := s.Events()
	if ch == nil {
		t.Fatal("Events() returned nil")
	}
	emitAssistantToken(s, "hi")
	select {
	case ev := <-ch:
		if ev.Kind != EventAssistantToken || ev.Text != "hi" {
			t.Errorf("unexpected event: %+v", ev)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Events() channel did not receive emitted event")
	}
}

func TestSessionIDAccessor(t *testing.T) {
	s := NewSession(SessionOpts{BufSize: 1})
	if got := s.ID(); got != 0 {
		t.Errorf("default ID = %d, want 0", got)
	}
	s.id = 99
	if got := s.ID(); got != 99 {
		t.Errorf("ID() = %d, want 99", got)
	}
}
