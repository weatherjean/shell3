package tui

import (
	"strings"
	"testing"

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

func TestColorizeEditOutputHighlightsCreatedPreviewMeta(t *testing.T) {
	input := strings.Join([]string{
		"Created /tmp/new.txt (+7 -0, 0→56 bytes)",
		"@@ -0,0 +1,7 @@",
		"+line-01",
		"… 2 created lines omitted",
	}, "\n")

	got := colorizeEditOutput(input)
	metaStyle := patchtui.BgRGB(74, 64, 24) + patchtui.Dim
	addStyle := patchtui.BgRGB(20, 60, 20) + patchtui.FgRGB(180, 230, 180)

	for _, want := range []string{
		metaStyle + "@@ -0,0 +1,7 @@" + patchtui.Reset,
		metaStyle + "… 2 created lines omitted" + patchtui.Reset,
		addStyle + "+line-01" + patchtui.Reset,
		patchtui.Dim + "Created /tmp/new.txt (+7 -0, 0→56 bytes)" + patchtui.Reset,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("colorized output missing %q:\n%q", want, got)
		}
	}
}
