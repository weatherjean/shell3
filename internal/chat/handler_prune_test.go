package chat

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
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

func TestPruneHandler_Execute_tooSmall(t *testing.T) {
	h := PruneHandler{}
	allMsgs := []llm.Message{
		{Role: llm.RoleTool, ToolCallID: "1", Content: "tiny"},
	}
	cfg := ToolConfig{AllMsgs: allMsgs, SessMsgs: allMsgs}
	args := json.RawMessage(`{"tool_call_id":"1","reason":"test"}`)
	out, _ := h.Execute(context.Background(), "1", args, cfg)
	if !strings.Contains(out, "below") {
		t.Fatalf("expected below-threshold message, got %q", out)
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

func TestLooksLikeError(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"error: something failed", true},
		{"Error something", true},
		{"[tool_call_id=1]\nerror: boom", true},
		{"[tool_call_id=1]\nok output", false},
		{"normal output", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := looksLikeError(tt.input); got != tt.want {
			t.Errorf("looksLikeError(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}
