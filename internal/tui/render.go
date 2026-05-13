package tui

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/weatherjean/shell3/pkg/chat"
	"github.com/weatherjean/shell3/internal/patchtui"
)

// dimLines wraps each non-empty line with dim+reset so the style is
// self-contained per line and doesn't bleed across slice boundaries.
func dimLines(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if l != "" {
			lines[i] = patchtui.Dim + l + patchtui.Reset
		}
	}
	return strings.Join(lines, "\n")
}

func isMemoryHistoryTool(name string) bool {
	switch name {
	case "memory_upsert", "memory_list", "memory_search", "history_get", "history_search":
		return true
	default:
		return false
	}
}

// toolCallHeader formats the colored "#id → name(args)" header shown above a
// tool's body in scrollback. Color picks reflect tool family: prune is pink,
// user tools are violet, memory/history queries are blue, everything else is
// muted-green.
func toolCallHeader(id, name, args string, isUserTool bool) string {
	color := patchtui.MutedGreen
	if name == "prune_tool_result" {
		color = patchtui.Pink
	} else if isUserTool {
		color = patchtui.Violet
	} else if isMemoryHistoryTool(name) {
		color = patchtui.Blue
	}

	if args == "" {
		return fmt.Sprintf("%s%s#%s → %s%s", color, patchtui.Bold, id, name, patchtui.Reset)
	}
	return fmt.Sprintf("%s%s#%s → %s(%s)%s", color, patchtui.Bold, id, name, args, patchtui.Reset)
}

// colorizeEditOutput renders +/- diff lines with red/green backgrounds so the
// TUI shows a git-diff-style preview. Hunk headers and omission markers get a
// faint yellow background so they stand out from dimmed context. Returns plain
// ANSI; not consumed by the model.
func colorizeEditOutput(s string) string {
	if s == "" {
		return s
	}
	bgAdd := patchtui.BgRGB(20, 60, 20)
	bgDel := patchtui.BgRGB(70, 20, 20)
	bgMeta := patchtui.BgRGB(74, 64, 24)
	fgAdd := patchtui.FgRGB(180, 230, 180)
	fgDel := patchtui.FgRGB(240, 180, 180)
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		switch {
		case isDiffAddedLine(l):
			lines[i] = bgAdd + fgAdd + l + patchtui.Reset
		case isDiffRemovedLine(l):
			lines[i] = bgDel + fgDel + l + patchtui.Reset
		case isDiffMetaLine(l):
			lines[i] = bgMeta + patchtui.Dim + l + patchtui.Reset
		case l != "":
			lines[i] = patchtui.Dim + l + patchtui.Reset
		}
	}
	return strings.Join(lines, "\n")
}

func isDiffAddedLine(line string) bool {
	return strings.HasPrefix(line, "+") && !strings.HasPrefix(line, "+++")
}

func isDiffRemovedLine(line string) bool {
	return strings.HasPrefix(line, "-") && !strings.HasPrefix(line, "---")
}

func isDiffMetaLine(line string) bool {
	return strings.HasPrefix(line, "@@ ") || (strings.HasPrefix(line, "… ") && strings.Contains(line, "created lines omitted"))
}

// summarizeEditArgs renders a one-line preview suitable for the TUI header.
func summarizeEditArgs(rawArgs string) string {
	var probe struct {
		FilePath string `json:"file_path"`
	}
	if err := json.Unmarshal([]byte(rawArgs), &probe); err != nil || probe.FilePath == "" {
		return rawArgs
	}
	return fmt.Sprintf(`file_path=%q`, probe.FilePath)
}

// truncateOutputMaxLines and truncateOutputMaxBytes cap how much of a tool
// result is shown inline in the TUI. The full result is always sent to the
// model; this only affects the user-visible display.
const (
	truncateOutputMaxLines = 3
	truncateOutputMaxBytes = 300
)

func truncateOutput(s string) string {
	lines := strings.Split(s, "\n")
	var kept []string
	used := 0
	for i, l := range lines {
		if i >= truncateOutputMaxLines {
			remaining := strings.Join(lines[i:], "\n")
			return strings.Join(kept, "\n") + fmt.Sprintf("\n… (+%d lines)\n", strings.Count(remaining, "\n")+1)
		}
		if used+len(l)+1 > truncateOutputMaxBytes {
			leftover := len(s) - used
			return strings.Join(kept, "\n") + fmt.Sprintf("\n… (+%d bytes)\n", leftover)
		}
		kept = append(kept, l)
		used += len(l) + 1
	}
	return s
}

// renderToolCallHeader produces the per-tool header line for a tool_call event.
// Bash family tools use the yellow $-prompt style; edit_file gets a one-line
// args summary; everything else uses the default colored toolCallHeader.
func renderToolCallHeader(ev chat.Event, cfg *chat.Config) string {
	switch ev.ToolName {
	case "bash":
		return fmt.Sprintf(patchtui.Yellow+patchtui.Bold+"#%s $ %s"+patchtui.Reset, ev.ToolCallID, chat.ParseBashArgs(ev.ToolInput))
	case "bash_bg":
		return fmt.Sprintf(patchtui.Red+patchtui.Bold+"#%s (bg)$"+patchtui.Reset+patchtui.Bold+" %s"+patchtui.Reset, ev.ToolCallID, chat.ParseBashArgs(ev.ToolInput))
	case "shell_interactive":
		return fmt.Sprintf(patchtui.Yellow+patchtui.Bold+"#%s $ %s"+patchtui.Reset+" (interactive)", ev.ToolCallID, chat.ParseBashArgs(ev.ToolInput))
	case "edit_file":
		return toolCallHeader(ev.ToolCallID, ev.ToolName, summarizeEditArgs(ev.ToolInput), false)
	case "compact_history":
		return toolCallHeader(ev.ToolCallID, ev.ToolName, "", false)
	default:
		_, isUserTool := cfg.UserTools[ev.ToolName]
		return toolCallHeader(ev.ToolCallID, ev.ToolName, ev.ToolInput, isUserTool)
	}
}

// renderToolResultBody returns the formatted body lines for a tool_result event.
// edit_file results pass through diff colorization; everything else is dimmed
// line-by-line. When truncate is false (default), large outputs are truncated
// for TUI display — the full output still went to the model via the tool message.
func renderToolResultBody(ev chat.Event, fullOutput bool) string {
	out := ev.ToolOutput
	if ev.ToolName == "edit_file" {
		return colorizeEditOutput(strings.TrimRight(out, "\n"))
	}
	if !fullOutput {
		out = truncateOutput(out)
	}
	return dimLines(strings.TrimRight(out, "\n"))
}
