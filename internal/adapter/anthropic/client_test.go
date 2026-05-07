package anthropic

import (
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
)

func TestParamSpecs(t *testing.T) {
	c := &Client{}
	specs := c.ParamSpecs()
	names := map[string]bool{}
	for _, s := range specs {
		names[s.Name] = true
	}
	for _, want := range []string{"reasoning_effort", "max_tokens", "temperature"} {
		if !names[want] {
			t.Errorf("missing param spec %q", want)
		}
	}
}

func TestToAnthropicMessages_Basic(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "hello"},
		{Role: llm.RoleAssistant, Content: "hi"},
	}
	out, system := toAnthropicMessages(msgs)
	if system != "" {
		t.Fatalf("expected no system, got %q", system)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2, got %d", len(out))
	}
}

func TestToAnthropicMessages_SystemExtracted(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "you are helpful"},
		{Role: llm.RoleUser, Content: "hello"},
	}
	out, system := toAnthropicMessages(msgs)
	if system != "you are helpful" {
		t.Fatalf("system: %q", system)
	}
	if len(out) != 1 {
		t.Fatalf("expected 1 non-system msg, got %d", len(out))
	}
}

func TestToAnthropicMessages_ToolResultGrouped(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{
			{ID: "tc1", Name: "bash", RawArgs: `{"cmd":"ls"}`},
			{ID: "tc2", Name: "bash", RawArgs: `{"cmd":"pwd"}`},
		}},
		{Role: llm.RoleTool, Content: "file.txt", ToolCallID: "tc1"},
		{Role: llm.RoleTool, Content: "/home", ToolCallID: "tc2"},
	}
	out, _ := toAnthropicMessages(msgs)
	if len(out) != 2 {
		t.Fatalf("expected 2 messages (assistant+grouped-user), got %d", len(out))
	}
}

func TestToAnthropicTools(t *testing.T) {
	tools := []llm.ToolDefinition{
		{
			Name:        "bash",
			Description: "run shell",
			Parameters: map[string]any{
				"type":     "object",
				"properties": map[string]any{
					"cmd": map[string]any{"type": "string"},
				},
				"required": []any{"cmd"},
			},
		},
	}
	out := toAnthropicTools(tools)
	if len(out) != 1 {
		t.Fatalf("expected 1, got %d", len(out))
	}
	tp := out[0].OfTool
	if tp == nil {
		t.Fatalf("expected OfTool set, got %+v", out[0])
	}
	if tp.Name != "bash" {
		t.Fatalf("name: %q", tp.Name)
	}
	if len(tp.InputSchema.Required) != 1 || tp.InputSchema.Required[0] != "cmd" {
		t.Fatalf("required: %+v", tp.InputSchema.Required)
	}
}
