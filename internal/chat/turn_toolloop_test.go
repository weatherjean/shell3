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

// stubHandler is a minimal ToolHandler that returns a fixed output string. An
// optional onExec hook runs first — tests use it to cancel the turn context
// mid-loop (the guard engine that used to drive cancellation was removed).
type stubHandler struct {
	name   string
	out    string
	onExec func()
}

func (h stubHandler) Name() string { return h.name }

func (h stubHandler) Execute(ctx context.Context, id string, args json.RawMessage, cfg ToolConfig) (string, error) {
	if h.onExec != nil {
		h.onExec()
	}
	return h.out, nil
}

func collectTurn(t *testing.T, ctx context.Context, cfg TurnConfig, input string) ([]Event, *Session) {
	t.Helper()
	sess, c := newCollectorSession(SessionOpts{})
	RunTurn(ctx, cfg, sess, llm.Message{Role: llm.RoleUser, Content: input}, nil)
	return c.all(), sess
}

func hasToolMessage(sess *Session, name, substr string) bool {
	for _, m := range sess.messages {
		if m.Role == llm.RoleTool && m.Name == name && strings.Contains(m.Content, substr) {
			return true
		}
	}
	return false
}

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
		ToolConfig:  ToolConfig{Log: LogOrNoop(nil)},
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

func TestRunTurn_UnknownTool(t *testing.T) {
	fake := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{
			{ToolCall: &llm.ToolCall{ID: "x", Name: "read_file", RawArgs: `{"path":"a.txt"}`}},
		}},
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "ok"}}},
	)
	cfg := TurnConfig{
		LLM:         fake,
		Personality: persona.Persona{SystemPrompt: "test"},
		Handlers:    map[string]ToolHandler{},
		ToolConfig:  ToolConfig{Log: LogOrNoop(nil)},
	}

	events, sess := collectTurn(t, context.Background(), cfg, "hi")

	var sawErr bool
	for _, ev := range events {
		if ev.Kind == EventToolResult && ev.ToolName == "read_file" && ev.ToolError &&
			strings.Contains(ev.ToolOutput, "bash-first") {
			sawErr = true
		}
	}
	if !sawErr {
		t.Fatalf("expected bash-first unknown-tool error event, got %+v", events)
	}
	if !hasToolMessage(sess, "read_file", "unknown tool") {
		t.Fatalf("expected unknown-tool message in session, got %+v", sess.messages)
	}
}

func hasKind(evs []Event, k EventKind) bool {
	for _, ev := range evs {
		if ev.Kind == k {
			return true
		}
	}
	return false
}

// TestRunTurn_MidLoopCtxCancel_EmitsError characterizes mid-loop cancellation:
// the first tool handler cancels the context, so the second iteration's
// top-of-loop ctx check trips and the turn ends with error (not turn_done).
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
		// The handler cancels during the first call; the next iteration's ctx
		// check trips and ends the turn with error.
		Handlers:   map[string]ToolHandler{"echo": stubHandler{name: "echo", out: "echoed", onExec: cancel}},
		ToolConfig: ToolConfig{Log: LogOrNoop(nil)},
	}

	events, _ := collectTurn(t, ctx, cfg, "hi")

	if !hasKind(events, EventError) {
		t.Fatalf("mid-loop ctx cancel should emit error; events=%+v", events)
	}
	if hasKind(events, EventTurnDone) {
		t.Fatalf("mid-loop ctx cancel should not emit turn_done")
	}
}

// TestRunTurn_MidLoopCtxCancel_PairsAllToolCalls pins the cancellation
// invariant: even when the context is cancelled mid-loop, every tool_call in
// the persisted assistant message gets exactly one matching RoleTool result.
// A gap leaves a dangling tool_call that makes the NEXT request 400
// ("tool call result does not follow tool call"). Calls "a" (executed before
// the cancel) and "b" (skipped by the cancel) must both be paired.
func TestRunTurn_MidLoopCtxCancel_PairsAllToolCalls(t *testing.T) {
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
		Handlers:    map[string]ToolHandler{"echo": stubHandler{name: "echo", out: "echoed", onExec: cancel}},
		ToolConfig:  ToolConfig{Log: LogOrNoop(nil)},
	}

	_, sess := collectTurn(t, ctx, cfg, "hi")

	wantIDs := map[string]bool{}
	gotIDs := map[string]bool{}
	for _, m := range sess.messages {
		if m.Role == llm.RoleAssistant {
			for _, tc := range m.ToolCalls {
				wantIDs[tc.ID] = true
			}
		}
		if m.Role == llm.RoleTool {
			if gotIDs[m.ToolCallID] {
				t.Fatalf("duplicate tool result for id %q", m.ToolCallID)
			}
			gotIDs[m.ToolCallID] = true
		}
	}
	for id := range wantIDs {
		if !gotIDs[id] {
			t.Fatalf("tool_call %q has no matching tool result; session=%+v", id, sess.messages)
		}
	}
	if len(gotIDs) != len(wantIDs) {
		t.Fatalf("result/tool_call count mismatch: results=%d tool_calls=%d", len(gotIDs), len(wantIDs))
	}
	// The skipped call is paired with a synthetic cancelled result.
	if !hasToolMessage(sess, "echo", "cancelled") {
		t.Fatalf("expected a synthetic cancelled result for the skipped call; session=%+v", sess.messages)
	}
}

func msgsContain(msgs []llm.Message, substr string) bool {
	for _, m := range msgs {
		if strings.Contains(m.Content, substr) {
			return true
		}
	}
	return false
}

func seedHistory(sess *Session, marker string, lastPromptTokens int) {
	sess.messages = []llm.Message{{Role: llm.RoleUser, Content: marker}}
	for i := 0; i < compactionFloor; i++ {
		sess.messages = append(sess.messages,
			llm.Message{Role: llm.RoleAssistant, Content: "older assistant turn"},
			llm.Message{Role: llm.RoleUser, Content: "older user turn"},
		)
	}
	sess.lastPromptTokens = lastPromptTokens
}

func TestRunTurn_AutoCompact_Triggers(t *testing.T) {
	fake := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{
			{TextDelta: "NARRATIVE SUMMARY of prior work"},
		}},
		fakellm.Script{Events: []llm.StreamEvent{
			{TextDelta: "answer"},
			{Usage: &llm.Usage{PromptTokens: 12, TotalTokens: 12}},
		}},
	)
	cfg := TurnConfig{
		LLM:         fake,
		Personality: persona.Persona{SystemPrompt: "test"},
		AgentKnobs:  AgentKnobs{CompactAt: 100},
		ToolConfig:  ToolConfig{Log: LogOrNoop(nil)},
	}

	sess, c := newCollectorSession(SessionOpts{})
	seedHistory(sess, "PRE_COMPACT_MARKER", 500)
	RunTurn(context.Background(), cfg, sess, llm.Message{Role: llm.RoleUser, Content: "new question"}, nil)
	events := c.all()

	if !hasKind(events, EventTurnDone) {
		t.Fatalf("auto-compact turn should complete with turn_done; events=%+v", events)
	}
	if fake.CallCount() != 2 {
		t.Fatalf("expected 2 LLM calls (compaction + turn), got %d", fake.CallCount())
	}
	for _, ev := range events {
		if ev.Kind == EventAssistantToken && strings.Contains(ev.Text, "NARRATIVE SUMMARY") {
			t.Fatalf("compaction summary leaked as an assistant token: %+v", ev)
		}
	}
	if !hasKind(events, EventCompacted) {
		t.Fatalf("expected an auto-compacted event; events=%+v", events)
	}
	turnPrompt := fake.Calls[1].Msgs
	if !msgsContain(turnPrompt, "NARRATIVE SUMMARY") {
		t.Fatalf("turn prompt missing compaction summary: %+v", turnPrompt)
	}
	if msgsContain(turnPrompt, "PRE_COMPACT_MARKER") {
		t.Fatalf("turn prompt still contains pre-compaction history: %+v", turnPrompt)
	}
}

func TestRunTurn_AutoCompact_Disabled(t *testing.T) {
	fake := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{
		{TextDelta: "answer"},
		{Usage: &llm.Usage{PromptTokens: 9, TotalTokens: 9}},
	}})
	cfg := TurnConfig{
		LLM:         fake,
		Personality: persona.Persona{SystemPrompt: "test"},
		AgentKnobs:  AgentKnobs{CompactAt: 0},
		ToolConfig:  ToolConfig{Log: LogOrNoop(nil)},
	}

	sess, _ := newCollectorSession(SessionOpts{})
	seedHistory(sess, "PRE_COMPACT_MARKER", 5000)
	RunTurn(context.Background(), cfg, sess, llm.Message{Role: llm.RoleUser, Content: "new question"}, nil)

	if fake.CallCount() != 1 {
		t.Fatalf("compact_at=0 must not compact; expected 1 LLM call, got %d", fake.CallCount())
	}
	if !msgsContain(fake.Calls[0].Msgs, "PRE_COMPACT_MARKER") {
		t.Fatalf("history should be untouched when compaction is disabled: %+v", fake.Calls[0].Msgs)
	}
}

func TestRunTurn_AutoCompact_FirstTurnNeverCompacts(t *testing.T) {
	fake := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{
		{TextDelta: "answer"},
		{Usage: &llm.Usage{PromptTokens: 9, TotalTokens: 9}},
	}})
	cfg := TurnConfig{
		LLM:         fake,
		Personality: persona.Persona{SystemPrompt: "test"},
		AgentKnobs:  AgentKnobs{CompactAt: 1},
		ToolConfig:  ToolConfig{Log: LogOrNoop(nil)},
	}

	sess, _ := newCollectorSession(SessionOpts{})
	seedHistory(sess, "PRE_COMPACT_MARKER", 0)
	RunTurn(context.Background(), cfg, sess, llm.Message{Role: llm.RoleUser, Content: "new question"}, nil)

	if fake.CallCount() != 1 {
		t.Fatalf("first turn must not compact; expected 1 LLM call, got %d", fake.CallCount())
	}
}

func TestRunTurn_AutoCompact_FailSafe(t *testing.T) {
	fake := fakellm.New(
		fakellm.Script{Err: errors.New("compaction stream blew up")},
		fakellm.Script{Events: []llm.StreamEvent{
			{TextDelta: "answer"},
			{Usage: &llm.Usage{PromptTokens: 12, TotalTokens: 12}},
		}},
	)
	cfg := TurnConfig{
		LLM:         fake,
		Personality: persona.Persona{SystemPrompt: "test"},
		AgentKnobs:  AgentKnobs{CompactAt: 100},
		ToolConfig:  ToolConfig{Log: LogOrNoop(nil)},
	}

	sess, c := newCollectorSession(SessionOpts{})
	seedHistory(sess, "PRE_COMPACT_MARKER", 500)
	RunTurn(context.Background(), cfg, sess, llm.Message{Role: llm.RoleUser, Content: "new question"}, nil)
	events := c.all()

	if !hasKind(events, EventTurnDone) {
		t.Fatalf("turn must complete despite compaction failure; events=%+v", events)
	}
	if hasKind(events, EventError) {
		t.Fatalf("compaction failure must not surface as a turn error; events=%+v", events)
	}
	if fake.CallCount() != 2 {
		t.Fatalf("expected the failed compaction call + the turn call, got %d", fake.CallCount())
	}
	if !msgsContain(fake.Calls[1].Msgs, "PRE_COMPACT_MARKER") {
		t.Fatalf("fail-safe: turn should run on un-compacted history: %+v", fake.Calls[1].Msgs)
	}
}

// TestRunTurn_CtxCancel_PreservesTypedError pins that cancellation surfaces as
// the typed context.Canceled (not a look-alike string error), so front-ends can
// errors.Is across the internal/shell3 boundary.
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
		Handlers:    map[string]ToolHandler{"echo": stubHandler{name: "echo", out: "echoed", onExec: cancel}},
		ToolConfig:  ToolConfig{Log: LogOrNoop(nil)},
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
