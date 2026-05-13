package chat

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/pkg/llm"
)

func TestPruneHandler_Name(t *testing.T) {
	if (PruneHandler{}).Name() != "prune_tool_result" {
		t.Fatal("wrong name")
	}
}

func TestPruneHandler_Execute_success(t *testing.T) {
	h := PruneHandler{}
	content := strings.Repeat("x", 600)
	allMsgs := []llm.Message{
		{Role: llm.RoleTool, ToolCallID: "42", Name: "bash", Content: content},
	}
	cfg := ToolConfig{AllMsgs: allMsgs, SessMsgs: allMsgs}
	args := json.RawMessage(`{"tool_call_id":"42","reason":"no longer needed"}`)

	out, err := h.Execute(context.Background(), "1", args, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "Pruned result of bash") {
		t.Fatalf("unexpected output: %q", out)
	}
	if !strings.HasPrefix(allMsgs[0].Content, "[pruned:") {
		t.Fatalf("expected content to be stubbed, got %q", allMsgs[0].Content)
	}
}

func TestPruneHandler_Execute_smallResult(t *testing.T) {
	h := PruneHandler{}
	allMsgs := []llm.Message{
		{Role: llm.RoleTool, ToolCallID: "1", Name: "bash", Content: "tiny"},
	}
	cfg := ToolConfig{AllMsgs: allMsgs, SessMsgs: allMsgs}
	args := json.RawMessage(`{"tool_call_id":"1","reason":"test"}`)
	out, err := h.Execute(context.Background(), "1", args, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "Pruned result of bash") {
		t.Fatalf("expected success even for small result, got %q", out)
	}
}

func TestPruneHandler_Execute_errorOutput(t *testing.T) {
	h := PruneHandler{}
	allMsgs := []llm.Message{
		{Role: llm.RoleTool, ToolCallID: "2", Name: "bash", Content: "error: something failed"},
	}
	cfg := ToolConfig{AllMsgs: allMsgs, SessMsgs: allMsgs}
	args := json.RawMessage(`{"tool_call_id":"2","reason":"pruning error output"}`)
	out, err := h.Execute(context.Background(), "1", args, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "Pruned result of bash") {
		t.Fatalf("expected success even for error output, got %q", out)
	}
}

func TestPruneHandler_Execute_missingID(t *testing.T) {
	h := PruneHandler{}
	cfg := ToolConfig{}
	args := json.RawMessage(`{"reason":"test"}`)
	out, _ := h.Execute(context.Background(), "1", args, cfg)
	if !strings.Contains(out, "tool_call_id required") {
		t.Fatalf("expected validation error, got %q", out)
	}
}

func TestPruneHandler_Execute_outOfScope(t *testing.T) {
	// Conversation: U1 → tool#1 → U2 → tool#2 → U3 → tool#3.
	// With scope=2 turns (back through 2 user messages), the boundary lands
	// at U2, so tool#1 is out of scope but tool#2 and tool#3 are in scope.
	h := PruneHandler{}
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "u1"},
		{Role: llm.RoleTool, ToolCallID: "1", Name: "bash", Content: "first"},
		{Role: llm.RoleUser, Content: "u2"},
		{Role: llm.RoleTool, ToolCallID: "2", Name: "bash", Content: "second"},
		{Role: llm.RoleUser, Content: "u3"},
		{Role: llm.RoleTool, ToolCallID: "3", Name: "bash", Content: "third"},
	}
	cfg := ToolConfig{AllMsgs: msgs, SessMsgs: msgs}

	// tool#1 — out of scope.
	out, _ := h.Execute(context.Background(), "x", json.RawMessage(`{"tool_call_id":"1","reason":"r"}`), cfg)
	if !strings.Contains(out, "older than the last 2 turns") {
		t.Fatalf("expected out-of-scope error for tool#1, got %q", out)
	}
	if msgs[1].Content != "first" {
		t.Fatalf("tool#1 should not have been mutated, got %q", msgs[1].Content)
	}

	// tool#2 — in scope (previous turn).
	out, _ = h.Execute(context.Background(), "x", json.RawMessage(`{"tool_call_id":"2","reason":"r"}`), cfg)
	if !strings.HasPrefix(out, "Pruned result of bash") {
		t.Fatalf("expected tool#2 prune to succeed, got %q", out)
	}

	// tool#3 — in scope (current turn).
	out, _ = h.Execute(context.Background(), "x", json.RawMessage(`{"tool_call_id":"3","reason":"r"}`), cfg)
	if !strings.HasPrefix(out, "Pruned result of bash") {
		t.Fatalf("expected tool#3 prune to succeed, got %q", out)
	}
}

func TestLastNTurns(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleUser}, {Role: llm.RoleAssistant},
		{Role: llm.RoleUser}, {Role: llm.RoleAssistant},
		{Role: llm.RoleUser}, {Role: llm.RoleAssistant},
	}
	if got := lastNTurns(msgs, 2); len(got) != 4 {
		t.Fatalf("n=2 want len 4, got %d", len(got))
	}
	if got := lastNTurns(msgs, 1); len(got) != 2 {
		t.Fatalf("n=1 want len 2, got %d", len(got))
	}
	if got := lastNTurns(msgs, 5); len(got) != len(msgs) {
		t.Fatalf("n>actual want full slice, got %d", len(got))
	}
	if got := lastNTurns(nil, 2); got != nil && len(got) != 0 {
		t.Fatalf("nil input should return empty/nil")
	}
}

func TestPruneHandler_Execute_notFound(t *testing.T) {
	h := PruneHandler{}
	cfg := ToolConfig{AllMsgs: []llm.Message{}, SessMsgs: []llm.Message{}}
	args := json.RawMessage(`{"tool_call_id":"99","reason":"test"}`)
	out, _ := h.Execute(context.Background(), "1", args, cfg)
	if !strings.Contains(out, "no tool result") {
		t.Fatalf("expected not-found error, got %q", out)
	}
}
