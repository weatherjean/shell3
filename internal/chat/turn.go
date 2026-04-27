package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/patchapp"
	"github.com/weatherjean/shell3/internal/patchtui"
)

// dumpStreamError writes the failing turn's messages and the last raw
// HTTP traffic to .shell3/last_error.json under cfg.WorkDir. Best-effort —
// any IO error is silently ignored.
func dumpStreamError(cfg Config, msgs []llm.Message, streamErr error) {
	if cfg.WorkDir == "" {
		return
	}
	var reqBody, resBody []byte
	if ts, ok := cfg.LLM.(llm.TrafficInspector); ok {
		reqBody, resBody = ts.LastTraffic()
	}
	rec := map[string]any{
		"timestamp":     time.Now().Format(time.RFC3339),
		"error":         streamErr.Error(),
		"messages":     msgs,
		"request_body":  string(reqBody),
		"response_body": string(resBody),
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return
	}
	path := filepath.Join(cfg.WorkDir, ".shell3", "last_error.json")
	_ = os.WriteFile(path, data, 0644)
}

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

// runTurn executes one user→assistant exchange, sending events to ch.
// The goroutine closes ch when done.
func runTurn(ctx context.Context, cfg Config, sess *session, input string, ch chan<- patchapp.Event) {
	defer close(ch)
	defer func() {
		if r := recover(); r != nil {
			err := fmt.Errorf("panic: %v", r)
			cfg.Hooks.OnError(ctx, err)
			ch <- patchapp.TurnErrEvent{Err: err}
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
		text, reasoning, providerReasoning, toolCalls, usage, err := streamOnce(ctx, cfg.LLM, allMsgs, cfg.Personality.Tools, ch)
		if err != nil {
			dumpStreamError(cfg, allMsgs, err)
			cfg.Hooks.OnError(ctx, err)
			ch <- patchapp.TurnErrEvent{Err: err}
			return
		}

		// Replace provider-emitted tool-call ids with sequential session-scoped
		// decimal ids ("1", "2", ...). Provider-native ids like "web_fetch:0"
		// get truncated by models when echoed back, breaking id-based tool
		// result addressing (e.g. prune_tool_result). A bare integer has no
		// separator to chop at. Provider just pairs ids by string match
		// between assistant.tool_calls[i].id and tool.tool_call_id, so the
		// rewrite is transparent on the wire.
		for i := range toolCalls {
			toolCalls[i].ID = sess.allocToolCallID()
		}

		if text != "" || len(toolCalls) > 0 || len(providerReasoning) > 0 {
			assistantMsg := llm.Message{
				Role:              llm.RoleAssistant,
				Content:           text,
				ReasoningContent:  reasoning,
				ProviderReasoning: providerReasoning,
			}
			assistantMsg.ToolCalls = toolCalls
			allMsgs = append(allMsgs, assistantMsg)
			sess.append(assistantMsg)
		}

		if len(toolCalls) == 0 {
			ch <- patchapp.TurnDoneEvent{Usage: usage}
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
				ch <- patchapp.AppendEvent{Text: fmt.Sprintf(patchtui.Yellow+patchtui.Bold+"#%s $ %s"+patchtui.Reset+"\n", tc.ID, command)}
				out = executeBash(ctx, command, cfg.WorkDir)
				display := truncateOutput(out)
				if cfg.Truncate {
					display = out
				}
				ch <- patchapp.AppendEvent{Text: dimLines(strings.TrimRight(display, "\n")) + "\n\n"}
			} else if tc.Name == "shell_interactive" {
				command := parseBashCommand(tc.RawArgs)
				ch <- patchapp.AppendEvent{Text: fmt.Sprintf(patchtui.Yellow+patchtui.Bold+"#%s $ %s"+patchtui.Reset+" (interactive)\n", tc.ID, command)}
				replyC := make(chan string, 1)
				ch <- patchapp.TTYExecEvent{Cmd: command, WorkDir: cfg.WorkDir, ReplyC: replyC}
				out = <-replyC
			} else if tc.Name == "prune_tool_result" {
				ch <- patchapp.AppendEvent{Text: fmt.Sprintf(patchtui.MutedGreen+patchtui.Bold+"#%s → %s(%s)"+patchtui.Reset+"\n", tc.ID, tc.Name, tc.RawArgs)}
				out = handlePruneToolResult(tc.RawArgs, allMsgs, sess.messages)
				ch <- patchapp.AppendEvent{Text: dimLines(strings.TrimRight(out, "\n")) + "\n\n"}
			} else if tc.Name == "edit_file" || tc.Name == "write_file" {
				ch <- patchapp.AppendEvent{Text: fmt.Sprintf(patchtui.MutedGreen+patchtui.Bold+"#%s → %s(%s)"+patchtui.Reset+"\n", tc.ID, tc.Name, summarizeEditArgs(tc.RawArgs))}
				out = handleEditTool(tc.Name, tc.RawArgs, cfg.WorkDir)
				ch <- patchapp.AppendEvent{Text: colorizeEditOutput(strings.TrimRight(out, "\n")) + "\n\n"}
			} else if tc.Name == "shell3_docs" {
				ch <- patchapp.AppendEvent{Text: fmt.Sprintf(patchtui.MutedGreen+patchtui.Bold+"#%s → shell3_docs"+patchtui.Reset+"\n", tc.ID)}
				out = cfg.Docs
				if out == "" {
					out = "Documentation not available."
				}
			} else if userTool, ok := cfg.UserTools[tc.Name]; ok {
				ch <- patchapp.AppendEvent{Text: fmt.Sprintf(patchtui.MutedGreen+patchtui.Bold+"#%s → %s(%s)"+patchtui.Reset+"\n", tc.ID, tc.Name, tc.RawArgs)}
				out = dispatchUserTool(ctx, userTool, tc.RawArgs, cfg.Secrets, cfg.WorkDir)
				display := truncateOutput(out)
				if cfg.Truncate {
					display = out
				}
				ch <- patchapp.AppendEvent{Text: dimLines(strings.TrimRight(display, "\n")) + "\n\n"}
			} else {
				ch <- patchapp.AppendEvent{Text: fmt.Sprintf(patchtui.MutedGreen+patchtui.Bold+"#%s → %s(%s)"+patchtui.Reset+"\n", tc.ID, tc.Name, tc.RawArgs)}
				out = dispatchStore(tc.Name, tc.RawArgs, cfg.Store)
				display := truncateOutput(out)
				if cfg.Truncate {
					display = out
				}
				ch <- patchapp.AppendEvent{Text: dimLines(strings.TrimRight(display, "\n")) + "\n\n"}
			}

			cfg.Hooks.OnToolResult(ctx, tc.Name, out)
			// Prepend the tool_call_id so the model has a stable handle it
			// can pass to prune_tool_result. Without this the id only lives
			// in structured metadata, which the model cannot reliably echo.
			content := fmt.Sprintf("[tool_call_id=%s]\n%s", tc.ID, out)
			toolMsg := llm.Message{
				Role:       llm.RoleTool,
				Content:    content,
				ToolCallID: tc.ID,
				Name:       tc.Name,
			}
			allMsgs = append(allMsgs, toolMsg)
			sess.append(toolMsg)
		}
	}
}

// streamOnce calls the LLM once, collecting text/reasoning/tool-calls/usage
// and emitting ChunkEvents on ch.
func streamOnce(ctx context.Context, client LLMClient, msgs []llm.Message, tools []llm.ToolDefinition, ch chan<- patchapp.Event) (text, reasoning string, providerReasoning []byte, toolCalls []llm.ToolCall, usage llm.Usage, err error) {
	var sb, rb strings.Builder
	streamErr := client.Stream(ctx, msgs, tools, func(ev llm.StreamEvent) {
		if ev.TextDelta != "" {
			sb.WriteString(ev.TextDelta)
			ch <- patchapp.ChunkEvent{Text: ev.TextDelta}
		}
		if ev.ReasoningDelta != "" {
			rb.WriteString(ev.ReasoningDelta)
		}
		if len(ev.ProviderReasoning) > 0 {
			providerReasoning = ev.ProviderReasoning
		}
		if ev.ToolCall != nil {
			toolCalls = append(toolCalls, *ev.ToolCall)
		}
		if ev.Usage != nil {
			usage = *ev.Usage
		}
	})
	if ctx.Err() != nil {
		return sb.String(), rb.String(), providerReasoning, toolCalls, usage, fmt.Errorf("context canceled")
	}
	return sb.String(), rb.String(), providerReasoning, toolCalls, usage, streamErr
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
