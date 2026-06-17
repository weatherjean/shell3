package chat

import "testing"

func TestEmitSessionStartEnd(t *testing.T) {
	s, c := newCollectorSession(SessionOpts{})
	s.id = "test-session-42"
	emitSessionStart(s, map[string]string{"persona": "default", "model": "gpt-x"})
	emitSessionEnd(s, "ok")

	got := c.all()
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2", len(got))
	}
	if got[0].Kind != EventSessionStart {
		t.Errorf("event[0].Kind = %v, want EventSessionStart", got[0].Kind)
	}
	if got[0].SessionID != "test-session-42" {
		t.Errorf("event[0].SessionID = %q, want test-session-42", got[0].SessionID)
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
