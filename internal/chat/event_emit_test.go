package chat

import "testing"

func TestEmitSessionEnd(t *testing.T) {
	s, c := newCollectorSession(SessionOpts{})
	s.id = "test-session-42"
	emitSessionEnd(s, "ok")

	got := c.all()
	if len(got) != 1 {
		t.Fatalf("got %d events, want 1", len(got))
	}
	if got[0].Kind != EventSessionEnd {
		t.Errorf("event[0].Kind = %v, want EventSessionEnd", got[0].Kind)
	}
	if got[0].SessionID != "test-session-42" {
		t.Errorf("event[0].SessionID = %q, want test-session-42", got[0].SessionID)
	}
	if got[0].Meta["status"] != "ok" {
		t.Errorf("event[0].Meta[status] = %q, want ok", got[0].Meta["status"])
	}
}
