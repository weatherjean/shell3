package chat

import (
	"testing"
	"time"
)

func TestEmitSessionStartEnd(t *testing.T) {
	s := NewSession(SessionOpts{BufSize: 4})
	s.id = 42
	emitSessionStart(s, map[string]string{"persona": "default", "model": "gpt-x"})
	emitSessionEnd(s, "ok")

	got := drainEvents(s, 2, 100*time.Millisecond)
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2", len(got))
	}
	if got[0].Kind != EventSessionStart {
		t.Errorf("event[0].Kind = %v, want EventSessionStart", got[0].Kind)
	}
	if got[0].SessionID != 42 {
		t.Errorf("event[0].SessionID = %d, want 42", got[0].SessionID)
	}
	if got[0].Meta["persona"] != "default" {
		t.Errorf("event[0].Meta[persona] = %q, want default", got[0].Meta["persona"])
	}
	if got[1].Kind != EventSessionEnd {
		t.Errorf("event[1].Kind = %v, want EventSessionEnd", got[1].Kind)
	}
	if got[1].Meta["status"] != "ok" {
		t.Errorf("event[1].Meta[status] = %q, want ok", got[1].Meta["status"])
	}
}

// drainEvents reads up to n events from s.events or returns whatever arrived
// before timeout.
func drainEvents(s *Session, n int, timeout time.Duration) []Event {
	out := make([]Event, 0, n)
	deadline := time.After(timeout)
	for len(out) < n {
		select {
		case ev := <-s.events:
			out = append(out, ev)
		case <-deadline:
			return out
		}
	}
	return out
}
