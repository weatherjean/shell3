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

// hasKind reports whether any event in evs has the given kind.
func hasKind(evs []Event, k EventKind) bool {
	for _, ev := range evs {
		if ev.Kind == k {
			return true
		}
	}
	return false
}

// TestRunTurn_GuardCancel_StubsRemainingCalls characterizes a guard cancel:
// round 1 returns two tool calls, the guard cancels, and the turn ends with a
// cancellation reminder + turn_done (not error). The first call gets a real
// "USER CANCELLED" result; the unreached second call gets a synthetic stub.
func TestRunTurn_GuardCancel_StubsRemainingCalls(t *testing.T) {
	fake := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{
			{ToolCall: &llm.ToolCall{ID: "a", Name: "echo", RawArgs: `{}`}},
			{ToolCall: &llm.ToolCall{ID: "b", Name: "echo", RawArgs: `{}`}},
			{Usage: &llm.Usage{TotalTokens: 5}},
		}},
	)
	sess := NewSession(SessionOpts{BufSize: 256})
	cfg := TurnConfig{
		LLM:         fake,
		Personality: persona.Persona{SystemPrompt: "test"},
		Handlers:    map[string]ToolHandler{"echo": stubHandler{name: "echo", out: "echoed"}},
		Log:         LogOrNoop(nil),
		ToolGuard: func(ctx context.Context, tool string, params map[string]any) (int, string, error) {
			return guardCancel, "nope", nil
		},
	}

	events := collectTurn(t, context.Background(), cfg, sess, "hi")

	if hasKind(events, EventError) {
		t.Fatalf("guard cancel should not emit error; events=%+v", events)
	}
	if !hasKind(events, EventTurnDone) {
		t.Fatalf("guard cancel should still emit turn_done")
	}
	var sawReminder bool
	for _, ev := range events {
		if ev.Kind == EventSystemReminder && strings.Contains(ev.Text, "turn cancelled by user") {
			sawReminder = true
		}
	}
	if !sawReminder {
		t.Fatalf("expected cancellation system reminder")
	}
	if !hasToolMessage(sess, "echo", "USER CANCELLED") {
		t.Fatalf("expected USER CANCELLED tool message for the first call")
	}
	if !hasToolMessage(sess, "echo", "Not executed") {
		t.Fatalf("expected synthetic stub tool message for the unreached call")
	}
}

// TestRunTurn_MidLoopCtxCancel_EmitsError characterizes mid-loop cancellation:
// the guard cancels the context during the first call, so the second
// iteration's top-of-loop ctx check trips and the turn ends with error (not
// turn_done). The guard returns allow — the abort comes from ctx, not the guard.
func TestRunTurn_MidLoopCtxCancel_EmitsError(t *testing.T) {
	fake := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{
			{ToolCall: &llm.ToolCall{ID: "a", Name: "echo", RawArgs: `{}`}},
			{ToolCall: &llm.ToolCall{ID: "b", Name: "echo", RawArgs: `{}`}},
			{Usage: &llm.Usage{TotalTokens: 5}},
		}},
	)
	sess := NewSession(SessionOpts{BufSize: 256})
	ctx, cancel := context.WithCancel(context.Background())
	cfg := TurnConfig{
		LLM:         fake,
		Personality: persona.Persona{SystemPrompt: "test"},
		Handlers:    map[string]ToolHandler{"echo": stubHandler{name: "echo", out: "echoed"}},
		Log:         LogOrNoop(nil),
		ToolGuard: func(c context.Context, tool string, params map[string]any) (int, string, error) {
			cancel() // cancel during the first call; the next iteration's ctx check trips
			return guardAllow, "", nil
		},
	}

	events := collectTurn(t, ctx, cfg, sess, "hi")

	if !hasKind(events, EventError) {
		t.Fatalf("mid-loop ctx cancel should emit error; events=%+v", events)
	}
	if hasKind(events, EventTurnDone) {
		t.Fatalf("mid-loop ctx cancel should not emit turn_done")
	}
}
