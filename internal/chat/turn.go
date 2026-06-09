package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/store"
)

// filterHeadlessTools returns tools with shell_interactive removed when
// headless is true. Other tools pass through unchanged.
func filterHeadlessTools(tools []llm.ToolDefinition, headless bool) []llm.ToolDefinition {
	if !headless {
		return tools
	}
	out := make([]llm.ToolDefinition, 0, len(tools))
	for _, td := range tools {
		if td.Name == "shell_interactive" {
			continue
		}
		out = append(out, td)
	}
	return out
}

// headlessReminder is injected once at the start of a headless turn so the
// model understands the environment. Adapters that block destructive tool
// calls also append their own reasons via the existing hook path.
const headlessReminder = "<system-reminder>\nheadless mode: no interactive shell, no human available to answer questions. Decide and proceed. Destructive commands may be blocked by host policy — if a block occurs, adapt rather than retry.\n</system-reminder>"

// logStreamError writes the failing turn's messages and the last raw HTTP
// traffic to .shell3/last_error.json under cfg.WorkDir, then records the
// event in the logger at Debug level (the TUI channel shows the error to the
// user, so stderr duplication is not needed here).
func logStreamError(cfg TurnConfig, msgs []llm.Message, streamErr error) {
	var reqBody, resBody []byte
	if ts, ok := cfg.LLM.(llm.TrafficInspector); ok {
		reqBody, resBody = ts.LastTraffic()
	}
	dumpPath := ""
	var dumpErr error
	if cfg.WorkDir != "" {
		rec := map[string]any{
			"timestamp":     time.Now().Format(time.RFC3339),
			"error":         streamErr.Error(),
			"messages":      msgs,
			"request_body":  string(reqBody),
			"response_body": string(resBody),
		}
		if data, err := json.MarshalIndent(rec, "", "  "); err == nil {
			p := filepath.Join(cfg.WorkDir, ".shell3", "last_error.json")
			if werr := os.WriteFile(p, data, 0644); werr != nil {
				// Don't advertise a dump file that wasn't written; surface the
				// write error instead so the failure is observable.
				dumpErr = werr
			} else {
				dumpPath = p
			}
		} else {
			dumpErr = err
		}
	}
	cfg.Log.Debug("stream error", "error", streamErr, "dump", dumpPath, "dump_error", dumpErr,
		"req_bytes", len(reqBody), "res_bytes", len(resBody))
}

// RunTurn executes one user→assistant turn, delivering chat.Events to the
// session sink synchronously as they occur. When RunTurn returns, every event
// (including the terminal turn_done/error) has been delivered.
//
// beforeDone, if non-nil, runs once at turn teardown immediately before the
// single terminal event (turn_done or error) is emitted — Session.Run uses it
// to persist history. The ordering matters: the terminal event is what embedders
// (pkg/shell3, the TUI) treat as "turn finished, safe to mutate session state",
// so any read of sess.messages in beforeDone must complete before it fires, or
// it races a concurrent SetMessages.
func RunTurn(ctx context.Context, cfg TurnConfig, sess *Session, userMsg llm.Message, beforeDone func()) {
	// terminalEmit holds the turn's single end event. It is emitted from the
	// deferred closure below, after beforeDone, so persistence happens-before
	// the done/error signal the embedder reacts to.
	var terminalEmit func()
	defer func() {
		if r := recover(); r != nil {
			stack := debug.Stack()
			err := fmt.Errorf("panic: %v\n%s", r, stack)
			cfg.Log.Error("panic in turn goroutine", err)
			terminalEmit = func() { emitError(sess, err) }
		}
		if beforeDone != nil {
			beforeDone()
		}
		if terminalEmit != nil {
			terminalEmit()
		}
	}()

	sess.append(userMsg)

	msgs := sess.messages

	allMsgs := make([]llm.Message, 0, len(msgs)+1)
	allMsgs = append(allMsgs, llm.Message{Role: llm.RoleSystem, Content: cfg.Personality.SystemPrompt})
	allMsgs = append(allMsgs, msgs...)

	toolList := filterHeadlessTools(cfg.Personality.Tools, cfg.Headless)
	if cfg.Headless {
		allMsgs = injectReminder(allMsgs, headlessReminder)
	}

	// Build schema index for fast lookup during tool call validation.
	toolSchemas := make(map[string]map[string]any, len(toolList))
	for _, td := range toolList {
		toolSchemas[td.Name] = td.Parameters
	}

	if reminder := sess.reminders.check(cfg.StatusLine, sess.lastPromptTokens); reminder != "" {
		allMsgs = injectReminder(allMsgs, reminder)
		emitSystemReminder(sess, reminder)
	}

	var totalUsage llm.Usage
	for {
		text, reasoning, toolCalls, usage, err := streamOnce(ctx, cfg.LLM, allMsgs, toolList, sess)
		if usage.TotalTokens > 0 || usage.PromptTokens > 0 || usage.CompletionTokens > 0 {
			totalUsage = addUsage(totalUsage, usage)
			emitUsage(sess, totalUsage.PromptTokens, totalUsage.CompletionTokens, totalUsage.TotalTokens)
		}
		if usage.PromptTokens > 0 {
			sess.lastPromptTokens = usage.PromptTokens
		}
		if err != nil {
			logStreamError(cfg, allMsgs, err)
			// Capture the typed error into a fresh local so terminalEmit carries
			// the value itself (errors.Is/As survives the public boundary), and so
			// the capture stays correct if this site is ever refactored away from
			// the immediate return.
			streamErr := err
			terminalEmit = func() { emitError(sess, streamErr) }
			return
		}
		if text != "" {
			emitAssistantMessage(sess, text)
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

		if text != "" || len(toolCalls) > 0 {
			assistantMsg := llm.Message{
				Role:             llm.RoleAssistant,
				Content:          text,
				ReasoningContent: reasoning,
			}
			assistantMsg.ToolCalls = toolCalls
			allMsgs = append(allMsgs, assistantMsg)
			sess.append(assistantMsg)
		}

		if len(toolCalls) == 0 {
			u := totalUsage
			terminalEmit = func() { emitTurnDone(sess, u.PromptTokens, u.CompletionTokens, u.TotalTokens) }
			return
		}

		// Execute tool calls. toolErr (distinct from the stream err above) is
		// non-nil only on context cancellation observed during the tool loop.
		outcome, toolErr := executeToolCalls(ctx, cfg, sess, toolCalls, toolSchemas, allMsgs)
		if toolErr != nil {
			turnErr := toolErr
			terminalEmit = func() { emitError(sess, turnErr) }
			return
		}
		allMsgs = outcome.allMsgs
		if outcome.cancelled {
			emitSystemReminder(sess, "[turn cancelled by user: "+outcome.cancelReason+"]")
			u := totalUsage
			terminalEmit = func() { emitTurnDone(sess, u.PromptTokens, u.CompletionTokens, u.TotalTokens) }
			return
		}

		// After all tool results are appended, check if a reminder is due
		// before the next LLM round. Inject into the last tool message in
		// allMsgs only — sess.messages stays clean.
		// Count bytes across all of allMsgs (including pruned replacements)
		// so prune is automatically reflected without any delta tracking.
		if reminder := sess.reminders.check(cfg.StatusLine, estimatePromptTokens(allMsgs)); reminder != "" {
			allMsgs[len(allMsgs)-1].Content += "\n\n" + reminder
			emitSystemReminder(sess, reminder)
		}

		// read_media results are text-only (tool messages can't carry media),
		// so any files it loaded are appended here as a synthetic user message —
		// the only role the adapter renders image/audio parts for. This runs
		// after the reminder block so the reminder lands on the last tool
		// message (text), not on this parts-only user message (whose Content the
		// adapter ignores).
		if len(outcome.pendingMedia) > 0 {
			parts := make([]llm.ContentPart, 0, len(outcome.pendingMedia)+1)
			parts = append(parts, outcome.pendingMedia...)
			parts = append(parts, llm.ContentPart{
				Type: llm.ContentPartTypeText,
				Text: "Above are the media file(s) you loaded with read_media.",
			})
			mediaMsg := llm.Message{
				Role:         llm.RoleUser,
				Content:      fmt.Sprintf("[read_media attached %d file(s)]", len(outcome.pendingMedia)),
				ContentParts: parts,
			}
			allMsgs = append(allMsgs, mediaMsg)
			sess.append(mediaMsg)
		}
	}
}

// toolLoopOutcome reports how a turn's tool-execution loop ended.
type toolLoopOutcome struct {
	allMsgs      []llm.Message     // updated slice (compact_history may have replaced it)
	cancelled    bool              // a guard returned a cancel decision
	cancelReason string            // reason text for the cancellation reminder
	pendingMedia []llm.ContentPart // media loaded by read_media, injected as a user message after the loop
}

// executeToolCalls runs the assistant's tool calls in order, emitting
// tool_call/tool_result events and appending each tool message to both allMsgs
// and the session. It returns the updated allMsgs plus cancellation state.
//
//   - a non-nil error means the context was cancelled mid-loop; the caller
//     emits an error terminal event and ends the turn.
//   - outcome.cancelled means a guard cancelled; the caller emits the
//     cancellation reminder and a turn_done terminal event.
//   - otherwise the loop completed normally; outcome.allMsgs carries the
//     updated message slice for the next round.
//
// Guard-cancel takes precedence over ctx-cancel: on a guard cancel it returns
// {cancelled:true}, nil without consulting ctx afterward.
func executeToolCalls(ctx context.Context, cfg TurnConfig, sess *Session, toolCalls []llm.ToolCall, toolSchemas map[string]map[string]any, allMsgs []llm.Message) (toolLoopOutcome, error) {
	var cancelled bool
	var cancelReason string
	var pendingMedia []llm.ContentPart
	for idx, tc := range toolCalls {
		if ctx.Err() != nil {
			return toolLoopOutcome{}, ctx.Err()
		}

		emitToolCall(sess, tc.ID, tc.Name, tc.RawArgs)
		var decision int
		var hookReason string
		var hookErr error
		if cfg.ToolGuard != nil {
			decision, hookReason, hookErr = cfg.ToolGuard(ctx, tc.Name, parseRawArgs(tc.RawArgs))
		}
		var out string
		if hookErr != nil {
			out = fmt.Sprintf("Tool-call hook failed (the on_tool_call hook script itself errored, not the user): %v. Do not retry the same call without adjusting your approach.", hookErr)
		} else if decision == guardCancel {
			if hookReason == "" {
				hookReason = "user cancelled"
			}
			cancelled = true
			cancelReason = hookReason
			out = fmt.Sprintf("USER CANCELLED the turn before this %s call ran. Reason: %s. Subsequent tool calls in this turn were not executed.", tc.Name, hookReason)
		} else if decision == guardBlock {
			if hookReason == "" {
				hookReason = "no reason given"
			}
			out = fmt.Sprintf("USER DENIED this %s tool call. Reason: %s. Treat this as the user explicitly disapproving this action — do NOT retry the same call. Acknowledge the denial, ask what they want instead, or pick a different approach.", tc.Name, hookReason)
		} else if schema, ok := toolSchemas[tc.Name]; ok {
			if err := validateToolArgs(schema, json.RawMessage([]byte(tc.RawArgs))); err != nil {
				out = fmt.Sprintf("error: invalid tool arguments: %v", err)
			}
		}
		// If a hook blocked the call or validation failed, out already carries
		// the reason text and we skip dispatch. The tool_result event emitted
		// below surfaces it with ToolError=true. Otherwise route to the tool.
		if out == "" {
			if tc.Name == "compact_history" {
				out, allMsgs = handleCompactHistory(tc.RawArgs, cfg.Store, sess, allMsgs, cfg.Log)
			} else if tc.Name == "shell_interactive" {
				command := ParseBashArgs(tc.RawArgs)
				if cfg.ShellInteractive != nil {
					out = cfg.ShellInteractive(ctx, command, cfg.WorkDir)
				} else {
					out = "error: interactive TTY not available"
				}
			} else if tc.Name == "read_media" {
				var part llm.ContentPart
				out, part = handleReadMedia(tc.RawArgs, cfg.WorkDir)
				if part.Type != "" {
					pendingMedia = append(pendingMedia, part)
				}
			} else if cfg.MCPToolNames[tc.Name] {
				out = dispatchMCPTool(ctx, cfg.MCPTool, tc.Name, tc.RawArgs)
			} else if cfg.CustomToolNames[tc.Name] {
				out = dispatchCustomTool(ctx, cfg.CustomTool, tc.Name, tc.RawArgs)
			} else if handler, ok := cfg.Handlers[tc.Name]; ok {
				toolCfg := ToolConfig{
					Store:    cfg.Store,
					WorkDir:  cfg.WorkDir,
					AllMsgs:  allMsgs,
					SessMsgs: sess.messages,
				}
				var herr error
				out, herr = handler.Execute(ctx, tc.ID, json.RawMessage([]byte(tc.RawArgs)), toolCfg)
				if herr != nil {
					// Most handlers encode failures in their output string and
					// return a nil error; a non-nil error is a genuine handler
					// fault (e.g. bash_bg failing to spawn). Log it, and if the
					// handler left no output, surface the error to the model as a
					// tool error rather than emitting an empty result.
					cfg.Log.Warn("tool handler error", "tool", tc.Name, "error", herr)
					if out == "" {
						out = "error: " + herr.Error()
					}
				}
			} else {
				out = fmt.Sprintf("error: unknown tool %q", tc.Name)
			}
		}

		emitToolResult(sess, tc.ID, tc.Name, out, isToolError(out))
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

		if cancelled {
			// Append synthetic results for any tool_calls we never reached
			// so the assistant message's tool_calls list has matching
			// tool_call_id results in history. Without this the next turn
			// 400s on providers that strictly validate the pairing.
			for _, rem := range toolCalls[idx+1:] {
				stub := llm.Message{
					Role:       llm.RoleTool,
					Content:    fmt.Sprintf("[tool_call_id=%s]\nNot executed — turn cancelled by user.", rem.ID),
					ToolCallID: rem.ID,
					Name:       rem.Name,
				}
				allMsgs = append(allMsgs, stub)
				sess.append(stub)
			}
			return toolLoopOutcome{allMsgs: allMsgs, cancelled: true, cancelReason: cancelReason}, nil
		}
	}

	if ctx.Err() != nil {
		return toolLoopOutcome{}, ctx.Err()
	}
	return toolLoopOutcome{allMsgs: allMsgs, pendingMedia: pendingMedia}, nil
}

// isToolError reports whether a tool's output string represents a failure,
// by its leading marker. Keep in sync with the markers produced in
// executeToolCalls (validation errors, guard block/cancel, hook failures).
func isToolError(out string) bool {
	return strings.HasPrefix(out, "error:") ||
		strings.HasPrefix(out, "USER DENIED") ||
		strings.HasPrefix(out, "USER CANCELLED") ||
		strings.HasPrefix(out, "Tool-call hook failed")
}

// streamOnce calls the LLM once, collecting text/reasoning/tool-calls/usage
// and emitting per-token chat.Events on sess.events.
func streamOnce(ctx context.Context, client LLMClient, msgs []llm.Message, tools []llm.ToolDefinition, sess *Session) (text, reasoning string, toolCalls []llm.ToolCall, usage llm.Usage, err error) {
	if ctx.Err() != nil {
		return "", "", nil, llm.Usage{}, ctx.Err()
	}
	var sb, rb strings.Builder
	streamErr := client.Stream(ctx, msgs, tools, func(ev llm.StreamEvent) {
		if ev.TextDelta != "" {
			sb.WriteString(ev.TextDelta)
			emitAssistantToken(sess, ev.TextDelta)
		}
		if ev.ReasoningDelta != "" {
			rb.WriteString(ev.ReasoningDelta)
			emitAssistantReasoning(sess, ev.ReasoningDelta)
		}
		if ev.ToolCall != nil {
			toolCalls = append(toolCalls, *ev.ToolCall)
		}
		if ev.Usage != nil {
			usage = *ev.Usage
		}
		if ev.Retry != nil {
			emitRetry(sess, ev.Retry)
		}
	})
	if ctx.Err() != nil {
		return sb.String(), rb.String(), toolCalls, usage, ctx.Err()
	}
	return sb.String(), rb.String(), toolCalls, usage, streamErr
}

// estimatePromptTokens approximates the token count for a message slice by
// summing content byte lengths and dividing by 4. allMsgs reflects pruning
// in-place, so this automatically accounts for freed context.
func estimatePromptTokens(msgs []llm.Message) int {
	var total int
	for _, m := range msgs {
		total += len(m.Content)
		for _, tc := range m.ToolCalls {
			total += len(tc.RawArgs)
		}
	}
	return total / 4
}

// addUsage accumulates token usage across the multiple LLM requests that can
// make up one agent turn when tools are involved.
// Each round re-sends the full context, so prompt tokens are not additive —
// only the latest round's prompt count is meaningful. Completion tokens are
// genuinely additive across rounds.
func addUsage(a, b llm.Usage) llm.Usage {
	completion := a.CompletionTokens + b.CompletionTokens
	return llm.Usage{
		PromptTokens:     b.PromptTokens,
		CompletionTokens: completion,
		TotalTokens:      b.PromptTokens + completion,
	}
}

func parseRawArgs(raw string) map[string]any {
	var out map[string]any
	_ = json.Unmarshal([]byte(raw), &out)
	return out
}

// saveHistory persists new messages to the store after a turn. Append failures
// are logged but not fatal — history is best-effort, but a silent drop would
// hide real faults (a full disk, a closed DB), so they surface via lg.
func saveHistory(st *store.Store, lg applog.Logger, sess *Session, sessionID int64, from int) {
	if st == nil {
		return
	}
	if from > len(sess.messages) {
		// compact_history rebuilt sess.messages from scratch; nothing new to save
		// (compact handler already wrote the summary to history directly).
		return
	}
	flushMessages(st, lg, sessionID, sess.messages[from:])
}

// flushMessages appends each user/assistant message in msgs to history under
// sessionID, plus one summary row per tool call. Best-effort: appendHistory
// logs any write failure rather than aborting. Shared by saveHistory (end of
// turn) and handleCompactHistory (flushing the outgoing session before roll).
func flushMessages(st *store.Store, lg applog.Logger, sessionID int64, msgs []llm.Message) {
	for _, m := range msgs {
		switch m.Role {
		case llm.RoleUser, llm.RoleAssistant:
			appendHistory(st, lg, sessionID, string(m.Role), m.Content)
			for _, tc := range m.ToolCalls {
				appendHistory(st, lg, sessionID, "tool", toolCallSummary(tc))
			}
		}
	}
}

// appendHistory writes one history row, logging on failure. Persistence is
// best-effort; the turn proceeds regardless of the outcome.
func appendHistory(st *store.Store, lg applog.Logger, sessionID int64, role, content string) {
	if err := st.AppendHistory(sessionID, role, content); err != nil {
		lg.Warn("append history failed", "session_id", sessionID, "role", role, "error", err)
	}
}

func toolCallSummary(tc llm.ToolCall) string {
	const maxLen = 80
	if tc.Name == "bash" {
		cmd := ParseBashArgs(tc.RawArgs)
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
