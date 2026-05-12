package chat

import (
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/patchtui"
)

func TestIsMemoryHistoryTool(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{name: "memory_upsert", want: true},
		{name: "memory_list", want: true},
		{name: "memory_search", want: true},
		{name: "history_get", want: true},
		{name: "history_search", want: true},
		{name: "prune_tool_result", want: false},
		{name: "bash", want: false},
	}

	for _, tt := range tests {
		if got := isMemoryHistoryTool(tt.name); got != tt.want {
			t.Errorf("isMemoryHistoryTool(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestToolCallHeaderColorByCategory(t *testing.T) {
	tests := []struct {
		name       string
		tool       string
		args       string
		isUserTool bool
		wantColor  string
	}{
		{name: "builtin", tool: "prune_tool_result", args: `{"tool_call_id":"1"}`, wantColor: patchtui.Pink},
		{name: "memory", tool: "memory_search", args: `{"terms":["go"]}`, wantColor: patchtui.Blue},
		{name: "user", tool: "brave_search", args: `{"query":"shell3"}`, isUserTool: true, wantColor: patchtui.Violet},
	}

	for _, tt := range tests {
		header := toolCallHeader("1", tt.tool, tt.args, tt.isUserTool)
		if !strings.HasPrefix(header, tt.wantColor+patchtui.Bold) {
			t.Errorf("%s: expected prefix with color+bold", tt.name)
		}
	}
}

func TestHeadless_StripsShellInteractiveTool(t *testing.T) {
	tools := []llm.ToolDefinition{
		{Name: "bash"},
		{Name: "shell_interactive"},
		{Name: "edit_file"},
	}
	out := filterHeadlessTools(tools, true)
	if len(out) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(out))
	}
	for _, td := range out {
		if td.Name == "shell_interactive" {
			t.Fatal("shell_interactive should have been stripped")
		}
	}
}

func TestHeadless_PassThroughWhenDisabled(t *testing.T) {
	tools := []llm.ToolDefinition{{Name: "bash"}, {Name: "shell_interactive"}}
	if got := filterHeadlessTools(tools, false); len(got) != 2 {
		t.Fatalf("expected pass-through, got %d", len(got))
	}
}
