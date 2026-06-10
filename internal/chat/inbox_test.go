package chat

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
	"github.com/weatherjean/shell3/internal/persona"
)

// TestInterject_IdleQueuesForNextTurn: an interject pushed while no turn is
// running is injected at the start of the next turn — visible to the model in
// the user message and surfaced as a SystemReminder event.
func TestInterject_IdleQueuesForNextTurn(t *testing.T) {
	fake := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "ok"}}})
	sess, c := newCollectorSession(SessionOpts{})
	sess.Interject("actually use repo B")

	cfg := TurnConfig{LLM: fake, Personality: persona.Persona{SystemPrompt: "t"}, Log: LogOrNoop(nil)}
	RunTurn(context.Background(), cfg, sess, llm.Message{Role: llm.RoleUser, Content: "hi"}, nil)

	events := c.all()
	var sawReminder bool
	for _, ev := range events {
		if ev.Kind == EventSystemReminder && strings.Contains(ev.Text, "actually use repo B") {
			sawReminder = true
		}
	}
	if !sawReminder {
		t.Fatalf("queued interject should surface as a system-reminder event; events=%+v", events)
	}
	// The model-visible injection lands on the turn's user message copy, not
	// on the session's persisted history.
	for _, m := range sess.messages {
		if strings.Contains(m.Content, "user interjected") {
			t.Fatalf("interject reminder leaked into persisted history: %q", m.Content)
		}
	}
}

// TestInterject_MidTurnInjectsNextRound: an interject pushed while a tool round
// is executing is delivered before the next LLM round.
func TestInterject_MidTurnInjectsNextRound(t *testing.T) {
	fake := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{
			{ToolCall: &llm.ToolCall{ID: "a", Name: "echo", RawArgs: `{}`}},
		}},
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "adjusted"}}},
	)
	sess, c := newCollectorSession(SessionOpts{})
	cfg := TurnConfig{
		LLM:         fake,
		Personality: persona.Persona{SystemPrompt: "t"},
		Handlers: map[string]ToolHandler{"echo": funcHandler{name: "echo",
			fn: func(context.Context, string, json.RawMessage, ToolConfig) (string, error) {
				sess.Interject("stop, wrong file")
				return "echoed", nil
			}}},
		Log: LogOrNoop(nil),
	}
	RunTurn(context.Background(), cfg, sess, llm.Message{Role: llm.RoleUser, Content: "go"}, nil)

	// Order: tool_result for echo, THEN the interject reminder, THEN tokens.
	events := c.all()
	toolIdx, remIdx := -1, -1
	for i, ev := range events {
		if ev.Kind == EventToolResult && toolIdx == -1 {
			toolIdx = i
		}
		if ev.Kind == EventSystemReminder && strings.Contains(ev.Text, "stop, wrong file") {
			remIdx = i
		}
	}
	if toolIdx == -1 || remIdx == -1 || remIdx < toolIdx {
		t.Fatalf("interject must inject after the tool round (tool=%d, reminder=%d)", toolIdx, remIdx)
	}
}
