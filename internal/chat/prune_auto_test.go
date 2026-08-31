package chat

import (
	"context"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/llm"
)

func bigTool(id string) llm.Message {
	return llm.Message{Role: llm.RoleTool, ToolCallID: id, Name: "bash", Content: strings.Repeat("x", 5000)}
}

func noopLogger() applog.Logger { return applog.Noop{} }

func testContext(t *testing.T) context.Context {
	t.Helper()
	return context.Background()
}

func TestMaybeCompact_TwoTierBands(t *testing.T) {
	mkSession := func() *Session {
		sess := newTestSession(t)
		sess.messages = []llm.Message{
			{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "1", Name: "bash"}}},
			bigTool("1"),
			{Role: llm.RoleUser, Content: strings.Repeat("r", 4000)},
		}
		return sess
	}
	cfg := TurnConfig{AgentKnobs: AgentKnobs{CompactAt: 1000, PruneAt: 600, KeepRecent: 1500}, ToolConfig: ToolConfig{Log: noopLogger()}}

	below := mkSession()
	below.lastPromptTokens = 100
	maybeCompact(testContext(t), cfg, below)
	if strings.HasPrefix(below.messages[1].Content, "[pruned") {
		t.Fatal("pruned below prune_at threshold")
	}

	inBand := mkSession()
	inBand.lastPromptTokens = 700
	maybeCompact(testContext(t), cfg, inBand)
	if !strings.HasPrefix(inBand.messages[1].Content, "[pruned") {
		t.Fatalf("in-band: old tool output not pruned (dispatcher skipped prune tier): %q", inBand.messages[1].Content)
	}
}

func TestPruneOldToolOutputs_StubsOldProtectsTail(t *testing.T) {
	sess := newTestSession(t)
	sess.messages = []llm.Message{
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "1", Name: "bash"}}},
		bigTool("1"),
		{Role: llm.RoleUser, Content: strings.Repeat("r", 4000)},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "2", Name: "bash"}}},
		bigTool("2"),
	}
	cfg := TurnConfig{AgentKnobs: AgentKnobs{CompactAt: 1000, KeepRecent: 1500}, ToolConfig: ToolConfig{Log: noopLogger()}}
	pruneOldToolOutputs(cfg, sess)

	if !strings.HasPrefix(sess.messages[1].Content, "[pruned") {
		t.Fatalf("old tool output not pruned: %q", sess.messages[1].Content)
	}
	if strings.HasPrefix(sess.messages[4].Content, "[pruned") {
		t.Fatal("recent tail tool output was pruned but should be protected")
	}
}
