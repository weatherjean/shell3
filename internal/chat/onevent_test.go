package chat

import (
	"testing"
)

func TestOnEventObservesEmittedEvents(t *testing.T) {
	var seen []EventKind
	s := NewSession(SessionOpts{
		OnEvent: func(ev Event) { seen = append(seen, ev.Kind) },
	})

	emitToolCall(s, "1", "bash", "{}")
	emitError(s, errBoom{})

	if len(seen) != 2 || seen[0] != EventToolCall || seen[1] != EventError {
		t.Fatalf("seen = %v, want [tool_call error]", seen)
	}
}

func TestOnEventFiresWithoutSink(t *testing.T) {
	n := 0
	s := NewSession(SessionOpts{OnEvent: func(Event) { n++ }})
	emitAssistantMessage(s, "hi")
	if n != 1 {
		t.Fatalf("OnEvent called %d times, want 1", n)
	}
}

func TestOnEventNilIsSafe(t *testing.T) {
	s := NewSession(SessionOpts{})
	emitAssistantMessage(s, "hi")
}

type errBoom struct{}

func (errBoom) Error() string { return "boom" }

// SetOnEvent repoints a live session's observer. The chat session outlives a
// config reload (it IS the history), so without this the observer stays bound
// to the generation that created it — and that generation's dispatcher is
// closed by the reload's teardown, silently ending delivery.
func TestSetOnEventRepointsObserver(t *testing.T) {
	first := 0
	s := NewSession(SessionOpts{OnEvent: func(Event) { first++ }})
	emitAssistantMessage(s, "before")

	second := 0
	s.SetOnEvent(func(Event) { second++ })
	emitAssistantMessage(s, "after")

	if first != 1 {
		t.Errorf("original observer saw %d events, want 1", first)
	}
	if second != 1 {
		t.Errorf("replacement observer saw %d events, want 1", second)
	}
}

// Clearing it is legal: a reloaded config may declare no event: block.
func TestSetOnEventNilStopsDelivery(t *testing.T) {
	n := 0
	s := NewSession(SessionOpts{OnEvent: func(Event) { n++ }})
	s.SetOnEvent(nil)
	emitAssistantMessage(s, "after")
	if n != 0 {
		t.Fatalf("observer saw %d events after being cleared, want 0", n)
	}
}
