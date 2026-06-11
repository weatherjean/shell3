package tui

import (
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/patchtui"
	"github.com/weatherjean/shell3/pkg/shell3"
)

func TestToolCallHeaderColorByCategory(t *testing.T) {
	tests := []struct {
		name       string
		tool       string
		args       string
		isUserTool bool
		wantColor  string
	}{
		{name: "builtin", tool: "edit_file", args: `{"file_path":"a"}`, wantColor: patchtui.MutedGreen},
		{name: "user", tool: "brave_search", args: `{"query":"shell3"}`, isUserTool: true, wantColor: patchtui.Violet},
	}

	for _, tt := range tests {
		header := toolCallHeader("1", tt.tool, tt.args, tt.isUserTool)
		if !strings.HasPrefix(header, tt.wantColor+patchtui.Bold) {
			t.Errorf("%s: expected prefix with color+bold", tt.name)
		}
	}
}

func TestParseBashArgs(t *testing.T) {
	if got := parseBashArgs(`{"command":"ls -la"}`); got != "ls -la" {
		t.Errorf("parseBashArgs extracted %q, want %q", got, "ls -la")
	}
	// Non-JSON / unparseable input falls back to the raw string, mirroring
	// chat.ParseBashArgs.
	if got := parseBashArgs("not json"); got != "not json" {
		t.Errorf("parseBashArgs fallback = %q, want raw passthrough", got)
	}
}

func TestRenderToolCallHeader_BashFamily(t *testing.T) {
	tests := []struct {
		tool string
		want string // substring that must appear
	}{
		{tool: "bash", want: "$ ls -la"},
		{tool: "bash_bg", want: "(bg)$"},
		{tool: "shell_interactive", want: "(interactive)"},
	}
	for _, tt := range tests {
		ev := shell3.Event{Kind: shell3.ToolCall, ToolName: tt.tool, ToolCallID: "7", ToolInput: `{"command":"ls -la"}`}
		got := renderToolCallHeader(ev)
		if !strings.Contains(got, tt.want) {
			t.Errorf("%s header = %q, want substring %q", tt.tool, got, tt.want)
		}
		if !strings.Contains(got, "#7") {
			t.Errorf("%s header missing id: %q", tt.tool, got)
		}
	}
}

func TestRenderToolCallHeader_CustomToolUsesEventFlag(t *testing.T) {
	// IsCustomTool comes off the public Event (resolved inside pkg/shell3), so a
	// user tool renders violet without any config lookup.
	ev := shell3.Event{Kind: shell3.ToolCall, ToolName: "brave_search", ToolCallID: "1", ToolInput: `{"q":"x"}`, IsCustomTool: true}
	got := renderToolCallHeader(ev)
	if !strings.HasPrefix(got, patchtui.Violet+patchtui.Bold) {
		t.Errorf("custom tool header not violet: %q", got)
	}
}

func TestRenderToolResultBody_EditFileColorizes(t *testing.T) {
	ev := shell3.Event{Kind: shell3.ToolResult, ToolName: "edit_file", ToolOutput: "@@ -0,0 +1,1 @@\n+added\n"}
	got := renderToolResultBody(ev)
	if !strings.Contains(got, "+added") {
		t.Errorf("edit_file body missing diff line: %q", got)
	}
	// Added line should carry the green add background style.
	if !strings.Contains(got, patchtui.BgRGB(20, 60, 20)) {
		t.Errorf("edit_file body not colorized: %q", got)
	}
}

func TestRenderToolResultBody_BashForwardsANSIColors(t *testing.T) {
	// An embedded SGR color must survive: not dimmed, not stripped. The body
	// must also end with Reset so a dangling color can't bleed into later lines.
	colored := "\033[31mred line\033[0m\nplain line"
	for _, tool := range []string{"bash", "bash_bg"} {
		ev := shell3.Event{Kind: shell3.ToolResult, ToolName: tool, ToolOutput: colored}
		got := renderToolResultBody(ev)
		if !strings.Contains(got, "\033[31mred line") {
			t.Errorf("%s: embedded ANSI color not preserved: %q", tool, got)
		}
		if strings.Contains(got, patchtui.Dim) {
			t.Errorf("%s: bash output was dimmed, want forwarded as-is: %q", tool, got)
		}
		if !strings.HasSuffix(got, patchtui.Reset) {
			t.Errorf("%s: bash body must end with Reset, got %q", tool, got)
		}
	}
}

func TestRenderToolResultBody_NonBashIsDimmed(t *testing.T) {
	ev := shell3.Event{Kind: shell3.ToolResult, ToolName: "read_media", ToolOutput: "some output"}
	got := renderToolResultBody(ev)
	if !strings.Contains(got, patchtui.Dim+"some output"+patchtui.Reset) {
		t.Errorf("non-bash tool output should be dimmed line-by-line, got %q", got)
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
