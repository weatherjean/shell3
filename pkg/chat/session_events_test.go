package chat

import "testing"

func TestSessionEventsChannelBuffered(t *testing.T) {
	s := NewSession(SessionOpts{BufSize: 16})
	if s.events == nil {
		t.Fatal("session.events is nil")
	}
	if cap(s.events) != 16 {
		t.Errorf("session.events cap = %d, want 16", cap(s.events))
	}
	for i := 0; i < 16; i++ {
		select {
		case s.events <- Event{Kind: EventAssistantToken}:
		default:
			t.Fatalf("event channel blocked at write %d (cap=%d)", i, cap(s.events))
		}
	}
}
