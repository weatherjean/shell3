package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/tui"
)

const (
	ansiBold   = "\033[1m"
	ansiYellow = "\033[33m"
	ansiDim    = "\033[2m"
	ansiReset  = "\033[0m"
)

// dimLines wraps each non-empty line with dim+reset so the style is
// self-contained per line and doesn't bleed across viewport slice boundaries.
func dimLines(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		if l != "" {
			lines[i] = ansiDim + l + ansiReset
		}
	}
	return strings.Join(lines, "\n")
}

// runTurn executes one user→assistant exchange, sending tui messages to ch.
// The goroutine closes ch when done.
func runTurn(ctx context.Context, cfg Config, sess *session, input string, ch chan<- tea.Msg) {
	defer close(ch)
	defer func() {
		if r := recover(); r != nil {
			ch <- tui.TurnErrMsg{Err: fmt.Errorf("panic: %v", r)}
		}
	}()

	cfg.Hooks.OnTurnStart(ctx)
	defer func() { cfg.Hooks.OnTurnEnd(ctx, "") }()

	sess.append(llm.Message{Role: llm.RoleUser, Content: input})

	msgs, err := cfg.Hooks.OnContextBuild(ctx, sess.messages)
	if err != nil {
		msgs = sess.messages
	}

	allMsgs := make([]llm.Message, 0, len(msgs)+1)
	allMsgs = append(allMsgs, llm.Message{Role: llm.RoleSystem, Content: cfg.Personality.SystemPrompt})
	allMsgs = append(allMsgs, msgs...)

	for {
		text, toolCalls, usage, err := streamOnce(ctx, cfg.LLM, allMsgs, cfg.Personality.Tools, ch)
		if err != nil {
			ch <- tui.TurnErrMsg{Err: err}
			return
		}

		if text != "" || len(toolCalls) > 0 {
			assistantMsg := llm.Message{Role: llm.RoleAssistant, Content: text}
			assistantMsg.ToolCalls = toolCalls
			allMsgs = append(allMsgs, assistantMsg)
			sess.append(assistantMsg)
		}

		if len(toolCalls) == 0 {
			ch <- tui.TurnDoneMsg{Usage: usage}
			return
		}

		// Execute tool calls.
		for _, tc := range toolCalls {
			if ctx.Err() != nil {
				return
			}

			allowed, hookErr := cfg.Hooks.OnToolCall(ctx, tc.Name, parseRawArgs(tc.RawArgs))
			var out string
			if hookErr != nil || !allowed {
				out = fmt.Sprintf("Tool call blocked: %v", hookErr)
			} else if tc.Name == "bash" {
				command := parseBashCommand(tc.RawArgs)
				ch <- tui.AppendMsg(fmt.Sprintf(ansiYellow+ansiBold+"$ %s"+ansiReset+"\n", command))
				out = executeBash(ctx, command, cfg.WorkDir)
				display := truncateOutput(out)
				if cfg.Truncate {
					display = out
				}
				ch <- tui.AppendMsg(dimLines(strings.TrimRight(display, "\n")) + "\n")
			} else if tc.Name == "shell_interactive" {
				command := parseBashCommand(tc.RawArgs)
				ch <- tui.AppendMsg(fmt.Sprintf(ansiYellow+ansiBold+"$ %s"+ansiReset+" (interactive)\n", command))
				replyC := make(chan string, 1)
				ch <- tui.TTYExecMsg{Cmd: command, WorkDir: cfg.WorkDir, ReplyC: replyC}
				out = <-replyC // blocks until the TUI finishes TTY handoff
			} else if tc.Name == "shell3_docs" {
				ch <- tui.AppendMsg(fmt.Sprintf(ansiBold+"→ shell3_docs"+ansiReset+"\n"))
				out = cfg.Docs
				if out == "" {
					out = "Documentation not available."
				}
			} else {
				ch <- tui.AppendMsg(fmt.Sprintf(ansiBold+"→ %s(%s)"+ansiReset+"\n", tc.Name, tc.RawArgs))
				out = dispatchStore(tc.Name, tc.RawArgs, cfg.Store)
				ch <- tui.AppendMsg(dimLines(strings.TrimRight(out, "\n")) + "\n")
			}

			cfg.Hooks.OnToolResult(ctx, tc.Name, out)
			toolMsg := llm.Message{
				Role:       llm.RoleTool,
				Content:    out,
				ToolCallID: tc.ID,
				Name:       tc.Name,
			}
			allMsgs = append(allMsgs, toolMsg)
			sess.append(toolMsg)
		}
	}
}

// streamOnce calls the LLM once, collecting text, tool calls, and usage while sending chunks to ch.
func streamOnce(ctx context.Context, client LLMClient, msgs []llm.Message, tools []llm.ToolDefinition, ch chan<- tea.Msg) (text string, toolCalls []llm.ToolCall, usage llm.Usage, err error) {
	var sb strings.Builder

	streamErr := client.Stream(ctx, msgs, tools, func(ev llm.StreamEvent) {
		if ev.TextDelta != "" {
			sb.WriteString(ev.TextDelta)
			ch <- tui.ChunkMsg(ev.TextDelta)
		}
		if ev.ToolCall != nil {
			toolCalls = append(toolCalls, *ev.ToolCall)
		}
		if ev.Usage != nil {
			usage = *ev.Usage
		}
	})

	if ctx.Err() != nil {
		return sb.String(), toolCalls, usage, fmt.Errorf("context canceled")
	}
	return sb.String(), toolCalls, usage, streamErr
}

func parseRawArgs(raw string) map[string]any {
	var out map[string]any
	json.Unmarshal([]byte(raw), &out)
	return out
}

// saveHistory persists new messages to the store after a turn.
func saveHistory(cfg Config, sess *session, sessionID int64, from int) {
	if cfg.Store == nil {
		return
	}
	for _, m := range sess.messages[from:] {
		switch m.Role {
		case llm.RoleUser, llm.RoleAssistant:
			cfg.Store.AppendHistory(sessionID, string(m.Role), m.Content)
			for _, tc := range m.ToolCalls {
				cfg.Store.AppendHistory(sessionID, "tool", toolCallSummary(tc))
			}
		}
	}
}

func toolCallSummary(tc llm.ToolCall) string {
	const maxLen = 80
	if tc.Name == "bash" {
		cmd := parseBashCommand(tc.RawArgs)
		line := strings.SplitN(cmd, "\n", 2)[0]
		if len(line) > maxLen {
			line = line[:maxLen] + "…"
		}
		return "bash: $ " + line
	}
	args := tc.RawArgs
	if len(args) > maxLen {
		args = args[:maxLen] + "…"
	}
	return tc.Name + "(" + args + ")"
}
