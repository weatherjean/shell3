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

func TestRunTurn_AutoCompact_TailWireValid_SecondTurn(t *testing.T) {
	fake := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "SUMMARY of prior work"}}},
		fakellm.Script{Events: []llm.StreamEvent{
			{TextDelta: "answer-1"},
			{Usage: &llm.Usage{PromptTokens: 5, TotalTokens: 5}},
		}},
		fakellm.Script{Events: []llm.StreamEvent{
			{TextDelta: "answer-2"},
			{Usage: &llm.Usage{PromptTokens: 5, TotalTokens: 5}},
		}},
	)
	cfg := TurnConfig{
		LLM:         fake,
		Personality: persona.Persona{SystemPrompt: "test"},
		AgentKnobs:  AgentKnobs{CompactAt: 100, KeepRecent: 25},
		ToolConfig:  ToolConfig{Log: LogOrNoop(nil)},
	}

	sess, c := newCollectorSession(SessionOpts{})
	big := strings.Repeat("y", 40)
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
	sess.lastPromptTokens = 500

	RunTurn(context.Background(), cfg, sess, llm.Message{Role: llm.RoleUser, Content: "q1"}, nil)

	if !hasKind(c.all(), EventCompacted) {
		t.Fatal("turn 1 should have compacted")
	}
	if sess.messages[0].Role != llm.RoleUser || !strings.Contains(sess.messages[0].Content, "SUMMARY") {
		t.Fatalf("first message must be the continuation summary, got %+v", sess.messages[0])
	}
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

	before := len(c.all())
	RunTurn(context.Background(), cfg, sess, llm.Message{Role: llm.RoleUser, Content: "q2"}, nil)
	if !hasKind(c.all()[before:], EventTurnDone) {
		t.Fatal("second turn on the rebuilt history should complete with turn_done")
	}
	assertNoOrphanToolResults(t, sess.messages)
}
