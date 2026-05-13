package chat

import (
	"testing"

	"github.com/weatherjean/shell3/pkg/llm"
)

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
