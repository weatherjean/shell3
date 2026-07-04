package chat

import (
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
)

func msg(role llm.Role, content string) llm.Message {
	return llm.Message{Role: role, Content: content}
}

func TestCompactionCut_KeepsTailByTokens(t *testing.T) {
	// Each message is 40 bytes ≈ 10 tokens. keepRecent=25 tokens should keep
	// the last 3 messages (10+10+10 >= 25) -> cut at index len-3.
	body := "0123456789012345678901234567890123456789" // 40 bytes
	msgs := []llm.Message{
		msg(llm.RoleUser, body), msg(llm.RoleAssistant, body),
		msg(llm.RoleUser, body), msg(llm.RoleAssistant, body),
		msg(llm.RoleUser, body),
	}
	got := compactionCut(msgs, 25)
	if got != 2 {
		t.Fatalf("cut = %d, want 2", got)
	}
}

func TestCompactionCut_ZeroKeepRecentReturnsLen(t *testing.T) {
	msgs := []llm.Message{msg(llm.RoleUser, "a"), msg(llm.RoleAssistant, "b")}
	if got := compactionCut(msgs, 0); got != len(msgs) {
		t.Fatalf("cut = %d, want %d", got, len(msgs))
	}
}

func TestCompactionCut_SnapsForwardOffOrphanToolResult(t *testing.T) {
	// Tail boundary lands on a tool message; it must snap forward so the tail
	// never begins with an orphan tool result.
	msgs := []llm.Message{
		msg(llm.RoleUser, "u"),
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "1", Name: "bash", RawArgs: "{}"}}},
		{Role: llm.RoleTool, ToolCallID: "1", Name: "bash", Content: "big-output-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		msg(llm.RoleAssistant, "done"),
	}
	// keepRecent=2: msgs[3] ("done") contributes 1 token < 2, so the walk-back
	// continues to index 2 (the tool result, ~10 tokens), which lands on a tool
	// message and MUST be snapped forward — exercising the orphan-snap invariant.
	cut := compactionCut(msgs, 2)
	if msgs[cut].Role == llm.RoleTool {
		t.Fatalf("cut landed on a tool message (index %d); tail would start with an orphan result", cut)
	}
}

func TestResolveKeepRecent(t *testing.T) {
	if got := resolveKeepRecent(TurnConfig{AgentKnobs: AgentKnobs{CompactAt: 1000, KeepRecent: 250}}); got != 250 {
		t.Fatalf("explicit = %d, want 250", got)
	}
	if got := resolveKeepRecent(TurnConfig{AgentKnobs: AgentKnobs{CompactAt: 1200, KeepRecent: 0}}); got != 396 {
		t.Fatalf("derived = %d, want round(1200*0.33)=396", got)
	}
}
