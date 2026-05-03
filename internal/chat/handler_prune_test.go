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

func TestPruneHandler_Execute_notFound(t *testing.T) {
	h := PruneHandler{}
	cfg := ToolConfig{AllMsgs: []llm.Message{}, SessMsgs: []llm.Message{}}
	args := json.RawMessage(`{"tool_call_id":"99","reason":"test"}`)
	out, _ := h.Execute(context.Background(), "1", args, cfg)
	if !strings.Contains(out, "no tool result") {
		t.Fatalf("expected not-found error, got %q", out)
	}
}
