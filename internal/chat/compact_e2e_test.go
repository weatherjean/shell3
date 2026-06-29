package chat

import (
	"context"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
	"github.com/weatherjean/shell3/internal/persona"
)

// assertNoOrphanToolResults fails if any tool message appears without a
// preceding assistant tool_call of the same id — the exact shape an
// OpenAI-compatible provider rejects with a 400. This is the wire-validity
// invariant tail-preserving compaction must never break.
func assertNoOrphanToolResults(t *testing.T, msgs []llm.Message) {
	t.Helper()
	declared := map[string]bool{}
	for _, m := range msgs {
		for _, tc := range m.ToolCalls {
			declared[tc.ID] = true
		}
		if m.Role == llm.RoleTool && !declared[m.ToolCallID] {
			t.Fatalf("orphan tool result id=%q (no preceding assistant tool_call); rebuilt history is not wire-valid", m.ToolCallID)
		}
	}
}

// TestRunTurn_AutoCompact_TailWireValid_SecondTurn closes the end-to-end gap the
// per-task reviews could only reason about: when the preserved tail contains a
// real assistant(tool_call)+tool(result) pair, a RunTurn compaction must keep
// that pair intact (the cut snaps so the tail never begins with an orphan tool
// result), AND a subsequent RunTurn must run cleanly on the rebuilt history.
func TestRunTurn_AutoCompact_TailWireValid_SecondTurn(t *testing.T) {
	fake := fakellm.New(
		// turn 1, call 0: the quiet compaction summary of the head.
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "SUMMARY of prior work"}}},
		// turn 1, call 1: the user's turn, answered against the compacted history.
		fakellm.Script{Events: []llm.StreamEvent{
			{TextDelta: "answer-1"},
			{Usage: &llm.Usage{PromptTokens: 5, TotalTokens: 5}},
		}},
		// turn 2: a fresh turn on the rebuilt history.
		fakellm.Script{Events: []llm.StreamEvent{
			{TextDelta: "answer-2"},
			{Usage: &llm.Usage{PromptTokens: 5, TotalTokens: 5}},
		}},
	)
	cfg := TurnConfig{
		LLM:         fake,
		Personality: persona.Persona{SystemPrompt: "test"},
		Log:         LogOrNoop(nil),
		CompactAt:   100,
		KeepRecent:  25, // keeps roughly the last three messages as the tail
	}

	sess, c := newCollectorSession(SessionOpts{})
	// Head: 12 tiny filler messages (clears compactionFloor, summarized away).
	// Tail: assistant(tool_call id=1) + tool(result id=1) + assistant — the
	// pair the cut must preserve. The tail messages are sized so KeepRecent=25
	// lands the cut just before the assistant tool_call.
	big := strings.Repeat("y", 40) // ~10 estimated tokens each
	var msgs []llm.Message
	for range 12 {
		msgs = append(msgs, llm.Message{Role: llm.RoleAssistant, Content: "h"})
	}
	msgs = append(msgs,
		llm.Message{Role: llm.RoleAssistant, Content: big, ToolCalls: []llm.ToolCall{{ID: "1", Name: "bash", RawArgs: `{"command":"ls"}`}}},
		llm.Message{Role: llm.RoleTool, ToolCallID: "1", Name: "bash", Content: big},
		llm.Message{Role: llm.RoleAssistant, Content: big},
	)
	sess.messages = msgs
	sess.lastPromptTokens = 500 // > CompactAt: compaction fires at turn start

	RunTurn(context.Background(), cfg, sess, llm.Message{Role: llm.RoleUser, Content: "q1"}, nil)

	if !hasKind(c.all(), EventCompacted) {
		t.Fatal("turn 1 should have compacted")
	}
	if sess.messages[0].Role != llm.RoleUser || !strings.Contains(sess.messages[0].Content, "SUMMARY") {
		t.Fatalf("first message must be the continuation summary, got %+v", sess.messages[0])
	}
	// The assistant tool_call from the tail must have survived intact (it lives
	// in ToolCalls, not Content, so check there).
	var keptToolCall bool
	for _, m := range sess.messages {
		for _, tc := range m.ToolCalls {
			if tc.ID == "1" {
				keptToolCall = true
			}
		}
	}
	if !keptToolCall {
		t.Fatalf("tail assistant tool_call (id=1) was dropped: %+v", sess.messages)
	}
	assertNoOrphanToolResults(t, sess.messages)

	// A fresh turn on the rebuilt history must complete cleanly.
	before := len(c.all())
	RunTurn(context.Background(), cfg, sess, llm.Message{Role: llm.RoleUser, Content: "q2"}, nil)
	if !hasKind(c.all()[before:], EventTurnDone) {
		t.Fatal("second turn on the rebuilt history should complete with turn_done")
	}
	assertNoOrphanToolResults(t, sess.messages)
}
