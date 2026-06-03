package chat

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
	"github.com/weatherjean/shell3/internal/persona"
)

// stubHandler is a minimal ToolHandler that returns a fixed output string.
type stubHandler struct {
	name string
	out  string
}

func (h stubHandler) Name() string { return h.name }

func (h stubHandler) Execute(ctx context.Context, id string, args json.RawMessage, cfg ToolConfig) (string, error) {
	return h.out, nil
}

// collectTurn runs RunTurn in a goroutine and returns every event up to and
// including the terminal turn_done/error event (or fails on timeout).
func collectTurn(t *testing.T, ctx context.Context, cfg TurnConfig, sess *Session, input string) []Event {
	t.Helper()
	go RunTurn(ctx, cfg, sess, llm.Message{Role: llm.RoleUser, Content: input}, nil)
	var out []Event
	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev := <-sess.Events():
			out = append(out, ev)
			if ev.Kind == EventTurnDone || ev.Kind == EventError {
				return out
			}
		case <-deadline:
			t.Fatalf("timed out waiting for terminal event after %d events", len(out))
			return out
		}
	}
}

// hasToolMessage reports whether the session has a RoleTool message for the
// named tool whose content contains substr.
func hasToolMessage(sess *Session, name, substr string) bool {
	for _, m := range sess.messages {
		if m.Role == llm.RoleTool && m.Name == name && strings.Contains(m.Content, substr) {
			return true
		}
	}
	return false
}

// TestRunTurn_ToolRoundTrip characterizes a normal tool round-trip: round 1
// returns one tool call, round 2 returns plain text, the turn ends with
// turn_done, and the tool's result is appended to session history.
func TestRunTurn_ToolRoundTrip(t *testing.T) {
	fake := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{
			{ToolCall: &llm.ToolCall{ID: "x", Name: "echo", RawArgs: `{"v":1}`}},
			{Usage: &llm.Usage{PromptTokens: 5, CompletionTokens: 2, TotalTokens: 7}},
		}},
		fakellm.Script{Events: []llm.StreamEvent{
			{TextDelta: "all done"},
			{Usage: &llm.Usage{PromptTokens: 6, CompletionTokens: 3, TotalTokens: 9}},
		}},
	)
	sess := NewSession(SessionOpts{BufSize: 256})
	cfg := TurnConfig{
		LLM:         fake,
		Personality: persona.Persona{SystemPrompt: "test"},
		Handlers:    map[string]ToolHandler{"echo": stubHandler{name: "echo", out: "echoed"}},
		Log:         LogOrNoop(nil),
	}

	events := collectTurn(t, context.Background(), cfg, sess, "hi")

	var sawCall, sawResult, sawDone bool
	for _, ev := range events {
		switch ev.Kind {
		case EventToolCall:
			if ev.ToolName == "echo" {
				sawCall = true
			}
		case EventToolResult:
			if ev.ToolName == "echo" && ev.ToolOutput == "echoed" && !ev.ToolError {
				sawResult = true
			}
		case EventTurnDone:
			sawDone = true
		}
	}
	if !sawCall || !sawResult || !sawDone {
		t.Fatalf("round-trip events: call=%v result=%v done=%v", sawCall, sawResult, sawDone)
	}
	if !hasToolMessage(sess, "echo", "echoed") {
		t.Fatalf("expected echo tool message in session, got %+v", sess.messages)
	}
	if fake.CallCount() != 2 {
		t.Fatalf("expected 2 LLM rounds, got %d", fake.CallCount())
	}
}
