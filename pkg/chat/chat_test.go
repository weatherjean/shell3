package chat

import (
	"strings"
	"testing"

	"github.com/weatherjean/shell3/pkg/llm"
)

// ── PruneLastTurn ──────────────────────────────────────────────────────────────

func TestPruneLastTurn(t *testing.T) {
	mk := func(role llm.Role, content string) llm.Message {
		return llm.Message{Role: role, Content: content}
	}
	u, a := llm.RoleUser, llm.RoleAssistant

	cases := []struct {
		name string
		in   []llm.Message
		want int // expected length
	}{
		{"empty", nil, 0},
		{"only assistant", []llm.Message{mk(a, "x")}, 1},
		{"single turn", []llm.Message{mk(u, "q"), mk(a, "r")}, 0},
		{"two turns", []llm.Message{mk(u, "q1"), mk(a, "r1"), mk(u, "q2"), mk(a, "r2")}, 2},
		{"trailing user only", []llm.Message{mk(a, "r1"), mk(u, "q2")}, 1},
	}
	for _, tc := range cases {
		got := PruneLastTurn(tc.in)
		if len(got) != tc.want {
			t.Errorf("%s: got len %d, want %d", tc.name, len(got), tc.want)
		}
	}
}

// ── handlePruneToolResult ──────────────────────────────────────────────────────

func mkToolMsg(id, name, content string) llm.Message {
	return llm.Message{Role: llm.RoleTool, ToolCallID: id, Name: name, Content: content}
}

func TestPruneToolResult_Success(t *testing.T) {
	big := strings.Repeat("x", 600)
	a := []llm.Message{mkToolMsg("tc1", "bash", big)}
	b := []llm.Message{mkToolMsg("tc1", "bash", big)}

	out := handlePruneToolResultFrom(`{"tool_call_id":"tc1","reason":"not needed"}`, a, b)
	if !strings.HasPrefix(out, "Pruned") {
		t.Fatalf("want success, got %q", out)
	}
	if !strings.Contains(a[0].Content, "[pruned:") || !strings.Contains(b[0].Content, "[pruned:") {
		t.Fatalf("content not updated in both slices: a=%q b=%q", a[0].Content, b[0].Content)
	}
}

func TestPruneToolResult_SmallResult(t *testing.T) {
	a := []llm.Message{mkToolMsg("tc1", "bash", "tiny output")}
	out := handlePruneToolResultFrom(`{"tool_call_id":"tc1","reason":"x"}`, a)
	if !strings.HasPrefix(out, "Pruned") {
		t.Fatalf("expected success even for small result, got %q", out)
	}
}

func TestPruneToolResult_ErrorOutput(t *testing.T) {
	body := "error: " + strings.Repeat("y", 600)
	a := []llm.Message{mkToolMsg("tc1", "bash", body)}
	out := handlePruneToolResultFrom(`{"tool_call_id":"tc1","reason":"x"}`, a)
	if !strings.HasPrefix(out, "Pruned") {
		t.Fatalf("expected success even for error output, got %q", out)
	}
}

func TestPruneToolResult_MissingID(t *testing.T) {
	a := []llm.Message{mkToolMsg("tc1", "bash", strings.Repeat("z", 600))}
	out := handlePruneToolResultFrom(`{"tool_call_id":"missing","reason":"x"}`, a)
	if !strings.Contains(out, "no tool result") {
		t.Fatalf("expected not-found error, got %q", out)
	}
}

func TestPruneToolResult_BadArgs(t *testing.T) {
	out := handlePruneToolResultFrom(`{"reason":"x"}`)
	if !strings.Contains(out, "tool_call_id required") {
		t.Fatalf("expected required-arg error, got %q", out)
	}
}
