package tui

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// The :background output view renders a subagent's structured --out transcript
// (this file) when the job has one, instead of the plain stdout log. The
// transcript carries reasoning, tool calls, and the final assistant message —
// none of which reach stdout — so it can colorize thinking, label tool calls,
// truncate tool output, and render the answer as markdown. Plain bash_bg jobs
// have no transcript and keep the wrapped stdout (see bgWrappedLines).

// transcriptEvent is the subset of a --out JSONL line the view renders. Unknown
// kinds and fields are ignored, and an unparseable line (a half-written tail, an
// unknown schema) is skipped — so a live, still-streaming transcript renders
// fine.
type transcriptEvent struct {
	Kind      string `json:"kind"`
	Text      string `json:"text"`
	Tool      string `json:"tool"`
	Input     string `json:"input"`
	Output    string `json:"output"`
	ToolError bool   `json:"tool_error"`
}

// toolResultMaxLines bounds how many lines of a tool result the transcript view
// shows before collapsing the rest into a "… +N more lines" marker.
const toolResultMaxLines = 12

// renderJobTranscript parses a subagent's --out transcript (JSONL) into wrapped
// display rows for the output view: the user prompt, reasoning in the thinking
// colour, tool calls labelled by tool with their input, tool results truncated
// and dimmed, and the assistant's answer as markdown. width is the content width
// to wrap to. Streaming assistant_token deltas are skipped — the final
// assistant_message is the form shown — as are lifecycle/usage events.
func renderJobTranscript(raw string, width int) []string {
	if width < 1 {
		width = 1
	}
	wrap := func(s string) string { return ansi.Wrap(strings.TrimRight(s, "\n"), width, " ") }

	var rows []string
	add := func(block string) {
		if strings.TrimSpace(block) == "" {
			return
		}
		if len(rows) > 0 {
			rows = append(rows, "") // blank spacer between blocks
		}
		rows = append(rows, strings.Split(strings.TrimRight(block, "\n"), "\n")...)
	}

	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var ev transcriptEvent
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue // half-written tail line or unknown schema — skip
		}
		switch ev.Kind {
		case "user_message":
			if t := strings.TrimSpace(ev.Text); t != "" {
				add(stUserPrompt.Render("› ") + stUserText.Render(wrap(t)))
			}
		case "assistant_reasoning":
			if t := strings.TrimSpace(ev.Text); t != "" {
				add(stThinking.Render("✲ thinking") + "\n" + stThinking.Render(wrap(t)))
			}
		case "assistant_message":
			if t := strings.TrimSpace(ev.Text); t != "" {
				add(strings.TrimRight(renderMarkdown(t, width), "\n"))
			}
		case "tool_call":
			block := toolStyle(ev.Tool).Render("● " + ev.Tool)
			if in := strings.TrimSpace(ev.Input); in != "" {
				block += "\n" + stDim.Render(wrap(in))
			}
			add(block)
		case "tool_result":
			add(renderToolResult(ev, wrap))
		}
	}
	return rows
}

// renderToolResult formats one tool result: a ✓/✗ tool label and the output
// truncated to toolResultMaxLines lines (dimmed), with a "… +N more lines"
// marker when it was cut.
func renderToolResult(ev transcriptEvent, wrap func(string) string) string {
	label := stTool.Render("✓ " + ev.Tool)
	if ev.ToolError {
		label = stErr.Render("✗ " + ev.Tool)
	}
	out := strings.TrimRight(ev.Output, "\n")
	if strings.TrimSpace(out) == "" {
		return label + "  " + stDim.Render("(no output)")
	}
	lines := strings.Split(out, "\n")
	more := 0
	if len(lines) > toolResultMaxLines {
		more = len(lines) - toolResultMaxLines
		lines = lines[:toolResultMaxLines]
	}
	block := label + "\n" + stDim.Render(wrap(strings.Join(lines, "\n")))
	if more > 0 {
		block += "\n" + stDim.Render(fmt.Sprintf("… +%d more lines", more))
	}
	return block
}
