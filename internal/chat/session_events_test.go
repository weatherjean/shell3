package chat

import "testing"

// TestNewSessionNilSinkIsSafe verifies that a Session built without a Sink
// installs a no-op, so emits never panic on a nil callback.
func TestNewSessionNilSinkIsSafe(t *testing.T) {
	s := NewSession(SessionOpts{})
	if s.sink == nil {
		t.Fatal("NewSession left sink nil; emits would panic")
	}
	emitAssistantToken(s, "hi") // must not panic
}

// TestNewSessionWiresSink verifies events reach the provided sink.
func TestNewSessionWiresSink(t *testing.T) {
	var got []Event
	s := NewSession(SessionOpts{Sink: func(ev Event) { got = append(got, ev) }})
	emitAssistantToken(s, "hi")
	if len(got) != 1 || got[0].Kind != EventAssistantToken || got[0].Text != "hi" {
		t.Fatalf("sink did not receive the event: %+v", got)
	}
}
