package chat

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

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

// collectTurn runs RunTurn against a fresh collector-backed session and returns
// every event it emits (delivery is synchronous, so all events are present once
// RunTurn returns) along with the session, so callers can inspect the resulting
// message history.
func collectTurn(t *testing.T, ctx context.Context, cfg TurnConfig, input string) ([]Event, *Session) {
	t.Helper()
	sess, c := newCollectorSession(SessionOpts{})
	RunTurn(ctx, cfg, sess, llm.Message{Role: llm.RoleUser, Content: input}, nil)
	return c.all(), sess
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
	cfg := TurnConfig{
		LLM:         fake,
		Personality: persona.Persona{SystemPrompt: "test"},
		Handlers:    map[string]ToolHandler{"echo": stubHandler{name: "echo", out: "echoed"}},
		Log:         LogOrNoop(nil),
	}

	events, sess := collectTurn(t, context.Background(), cfg, "hi")

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
	cfg := TurnConfig{
		LLM:         fake,
		Personality: persona.Persona{SystemPrompt: "test"},
		Handlers:    map[string]ToolHandler{"echo": stubHandler{name: "echo", out: "echoed"}},
		Log:         LogOrNoop(nil),
		ToolGuard: func(ctx context.Context, tool string, params map[string]any) (int, string, error) {
			return guardCancel, "nope", nil
		},
	}

	events, sess := collectTurn(t, context.Background(), cfg, "hi")

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
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
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

	events, _ := collectTurn(t, ctx, cfg, "hi")

	if !hasKind(events, EventError) {
		t.Fatalf("mid-loop ctx cancel should emit error; events=%+v", events)
	}
	if hasKind(events, EventTurnDone) {
		t.Fatalf("mid-loop ctx cancel should not emit turn_done")
	}
}

// msgsContain reports whether any message's content contains substr.
func msgsContain(msgs []llm.Message, substr string) bool {
	for _, m := range msgs {
		if strings.Contains(m.Content, substr) {
			return true
		}
	}
	return false
}

// TestRunTurn_CompactHistory_ReplacesAllMsgs characterizes the compact_history
// path: it replaces allMsgs in place, so the second round's prompt carries the
// compact summary and not the pre-compaction user text. Runs with no Store
// (handleCompactHistory skips store rolling when st == nil).
func TestRunTurn_CompactHistory_ReplacesAllMsgs(t *testing.T) {
	fake := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{
			{ToolCall: &llm.ToolCall{ID: "c", Name: "compact_history", RawArgs: `{"summary":"did stuff"}`}},
			{Usage: &llm.Usage{TotalTokens: 5}},
		}},
		fakellm.Script{Events: []llm.StreamEvent{
			{TextDelta: "continued"},
			{Usage: &llm.Usage{TotalTokens: 6}},
		}},
	)
	cfg := TurnConfig{
		LLM:         fake,
		Personality: persona.Persona{SystemPrompt: "test"},
		Log:         LogOrNoop(nil),
	}

	events, _ := collectTurn(t, context.Background(), cfg, "hello there")

	if !hasKind(events, EventTurnDone) {
		t.Fatalf("compact_history turn should complete with turn_done; events=%+v", events)
	}
	if fake.CallCount() != 2 {
		t.Fatalf("expected 2 LLM rounds, got %d", fake.CallCount())
	}
	round2 := fake.Calls[1].Msgs
	if !msgsContain(round2, "did stuff") {
		t.Fatalf("round 2 prompt missing compact summary: %+v", round2)
	}
	if msgsContain(round2, "hello there") {
		t.Fatalf("round 2 prompt still contains pre-compaction user text: %+v", round2)
	}
}

// TestRunTurn_CtxCancel_PreservesTypedError pins that cancellation surfaces as
// the typed context.Canceled (not a look-alike string error), so embedders can
// errors.Is across the pkg/shell3 boundary.
func TestRunTurn_CtxCancel_PreservesTypedError(t *testing.T) {
	fake := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{
			{ToolCall: &llm.ToolCall{ID: "a", Name: "echo", RawArgs: `{}`}},
			{ToolCall: &llm.ToolCall{ID: "b", Name: "echo", RawArgs: `{}`}},
		}},
	)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg := TurnConfig{
		LLM:         fake,
		Personality: persona.Persona{SystemPrompt: "test"},
		Handlers:    map[string]ToolHandler{"echo": stubHandler{name: "echo", out: "echoed"}},
		Log:         LogOrNoop(nil),
		ToolGuard: func(c context.Context, tool string, params map[string]any) (int, string, error) {
			cancel()
			return guardAllow, "", nil
		},
	}

	events, _ := collectTurn(t, ctx, cfg, "hi")

	var errEv *Event
	for i := range events {
		if events[i].Kind == EventError {
			errEv = &events[i]
			break
		}
	}
	if errEv == nil {
		t.Fatalf("expected an error event; events=%+v", events)
	}
	if !errors.Is(errEv.Err, context.Canceled) {
		t.Fatalf("error event should satisfy errors.Is(err, context.Canceled); got %v", errEv.Err)
	}
}
