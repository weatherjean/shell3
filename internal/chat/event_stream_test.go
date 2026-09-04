package chat

import (
	"errors"
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
)

func TestEmitAssistantToken(t *testing.T) {
	s, c := newCollectorSession(SessionOpts{})
	emitAssistantToken(s, "Hel")
	emitAssistantToken(s, "lo")
	got := c.all()
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2", len(got))
	}
	if got[0].Kind != EventAssistantToken || got[0].Text != "Hel" {
		t.Errorf("event[0]: %+v", got[0])
	}
	if got[1].Kind != EventAssistantToken || got[1].Text != "lo" {
		t.Errorf("event[1]: %+v", got[1])
	}
}

func TestEmitError(t *testing.T) {
	s, c := newCollectorSession(SessionOpts{})
	emitError(s, errors.New("boom"))
	got := c.all()
	if len(got) != 1 || got[0].Kind != EventError || got[0].Text != "boom" {
		t.Fatalf("error event mismatch: %+v", got)
	}
	if got[0].Err == nil || got[0].Err.Error() != "boom" {
		t.Fatalf("error event Err mismatch: %+v", got[0].Err)
	}
}

func TestEmitUsage(t *testing.T) {
	s, c := newCollectorSession(SessionOpts{})
	emitUsage(s, llm.Usage{PromptTokens: 100, CompletionTokens: 50, TotalTokens: 150, CachedTokens: 80})
	got := c.all()
	if len(got) != 1 || got[0].Kind != EventUsage {
		t.Fatalf("usage event missing: %+v", got)
	}
	if got[0].Usage == nil || got[0].Usage.PromptTokens != 100 || got[0].Usage.CompletionTokens != 50 || got[0].Usage.TotalTokens != 150 {
		t.Errorf("usage data: %+v", got[0].Usage)
	}
	if got[0].Usage.CachedTokens != 80 {
		t.Errorf("cached tokens must ride along: %+v", got[0].Usage)
	}
}

func TestEmitAssistantReasoning(t *testing.T) {
	s, c := newCollectorSession(SessionOpts{})
	emitAssistantReasoning(s, "thinking...")
	got := c.all()
	if len(got) != 1 || got[0].Kind != EventAssistantReasoning || got[0].Text != "thinking..." {
		t.Fatalf("assistant_reasoning event mismatch: %+v", got)
	}
}

func TestEmitTurnDone(t *testing.T) {
	s, c := newCollectorSession(SessionOpts{})
	emitTurnDone(s, llm.Usage{PromptTokens: 10, CompletionTokens: 20, TotalTokens: 30})
	got := c.all()
	if len(got) != 1 || got[0].Kind != EventTurnDone {
		t.Fatalf("turn_done event missing: %+v", got)
	}
	if got[0].Usage == nil || got[0].Usage.TotalTokens != 30 {
		t.Errorf("usage data: %+v", got[0].Usage)
	}
}

func TestSinkDeliversEveryEventInOrder(t *testing.T) {
	s, c := newCollectorSession(SessionOpts{})
	const n = 1000
	for range n {
		emitAssistantToken(s, "x")
	}
	emitTurnDone(s, llm.Usage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3})
	got := c.all()
	if len(got) != n+1 {
		t.Fatalf("delivered %d events, want %d (no drops)", len(got), n+1)
	}
	if got[n].Kind != EventTurnDone {
		t.Errorf("last event = %v, want EventTurnDone", got[n].Kind)
	}
}
