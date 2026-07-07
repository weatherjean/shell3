package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/runs"
)

// The background-jobs output view renders a subagent's messages.jsonl transcript
// (this file) when the job has one, instead of the plain stdout log. The
// transcript carries user turns, reasoning, tool calls, tool results, and the
// final assistant message — all of which are stored in the child session's
// messages.jsonl as llm.Message records. Plain bash_bg jobs have no transcript
// and keep the wrapped stdout path (see bgWrappedLines).

// toolResultMaxLines bounds how many lines of a tool result the transcript view
// shows before collapsing the rest into a "… +N more lines" marker.
const toolResultMaxLines = 12

// renderJobTranscript parses a subagent's messages.jsonl (one llm.Message JSON
// record per line) into wrapped display rows for the output view: user prompts,
// reasoning in the thinking colour, tool calls labelled by tool with their
// args, tool results truncated and dimmed, and the assistant's answer as
// markdown. width is the content width to wrap to. Unparseable lines and
// system messages are skipped so a live, still-streaming transcript renders
// fine.
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

	for _, msg := range runs.ParseMessages(raw) {
		switch msg.Role {
		case llm.RoleSystem:
			// skip system prompts — they are not meaningful to the human reader

		case llm.RoleUser:
			if t := strings.TrimSpace(msg.Content); t != "" {
				add(stUserPrompt.Render("› ") + stUserText.Render(wrap(t)))
			}

		case llm.RoleAssistant:
			// Reasoning (thinking) comes first in display order.
			if t := strings.TrimSpace(msg.ReasoningContent); t != "" {
				add(stThinking.Render("✲ thinking") + "\n" + stThinking.Render(wrap(t)))
			}
			// Tool calls (may be multiple in one assistant message).
			for _, tc := range msg.ToolCalls {
				block := toolStyle(tc.Name).Render("● " + tc.Name)
				if in := strings.TrimSpace(tc.RawArgs); in != "" {
					block += "\n" + stDim.Render(wrap(in))
				}
				add(block)
			}
			// Text content: the final answer or a preamble before tool calls.
			if t := strings.TrimSpace(msg.Content); t != "" {
				add(strings.TrimRight(renderMarkdown(t, width), "\n"))
			}

		case llm.RoleTool:
			add(renderMsgToolResult(msg, wrap))
		}
	}
	return rows
}

// renderMsgToolResult formats one tool-result message: a ✓ tool label and the
// output truncated to toolResultMaxLines lines (dimmed), with a "… +N more
// lines" marker when it was cut. msg.Name is the tool name; msg.Content is the
// raw output.
func renderMsgToolResult(msg llm.Message, wrap func(string) string) string {
	label := stTool.Render("✓ " + msg.Name)
	out := strings.TrimRight(msg.Content, "\n")
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
