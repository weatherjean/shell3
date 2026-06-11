package chat

import (
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
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

// ── PruneByID (host-side /prune slash command) ─────────────────────────────────

func mkToolMsg(id, name, content string) llm.Message {
	return llm.Message{Role: llm.RoleTool, ToolCallID: id, Name: name, Content: content}
}

func TestPruneByID_Success(t *testing.T) {
	big := strings.Repeat("x", 600)
	a := []llm.Message{mkToolMsg("tc1", "bash", big)}
	b := []llm.Message{mkToolMsg("tc1", "bash", big)}

	out, ok := PruneByID("tc1", "pruned by user", a, b)
	if !ok || !strings.HasPrefix(out, "Pruned") {
		t.Fatalf("want success, got ok=%v %q", ok, out)
	}
	if !strings.Contains(a[0].Content, "[pruned by user") || !strings.Contains(b[0].Content, "[pruned by user") {
		t.Fatalf("content not updated in both slices: a=%q b=%q", a[0].Content, b[0].Content)
	}
}

func TestPruneByID_NotFound(t *testing.T) {
	a := []llm.Message{mkToolMsg("tc1", "bash", strings.Repeat("z", 600))}
	out, ok := PruneByID("missing", "pruned by user", a)
	if ok || !strings.Contains(out, "no tool result") {
		t.Fatalf("expected not-found, got ok=%v %q", ok, out)
	}
}
