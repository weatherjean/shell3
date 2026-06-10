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
	texts, userParts := sess.drainInbox()
	if reminder := interjectReminder(texts); reminder != "" {
		allMsgs = injectReminder(allMsgs, reminder)
		emitSystemReminder(sess, reminder)
	}
	// Parts queued while idle are injected as a user message right after the
	// reminder lands on the turn's user message (consecutive user messages are
	// fine on the wire; only user messages can carry media parts).
	if msg, ok := attachmentsMessage(nil, userParts); ok {
		allMsgs = append(allMsgs, msg)
		sess.append(msg)
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
		texts, userParts := sess.drainInbox()
		if reminder := interjectReminder(texts); reminder != "" {
			allMsgs[len(allMsgs)-1].Content += "\n\n" + reminder
			emitSystemReminder(sess, reminder)
		}

		// read_media results are text-only (tool messages can't carry media), so
		// files it loaded — plus any attachments the user interjected during the
		// round — are appended here as a synthetic user message, the only role
		// the adapter renders image/audio parts for. This runs after the
		// reminder block so the reminder lands on the last tool message (text),
		// not on this parts-carrying user message.
		if msg, ok := attachmentsMessage(outcome.pendingMedia, userParts); ok {
			allMsgs = append(allMsgs, msg)
			sess.append(msg)
		}
	}
}

// attachmentsMessage builds the synthetic user message that delivers media
// parts mid-conversation: read_media loads from the last tool round and/or
// attachments the user sent via Interject. Tool messages can't carry media
// and the adapter renders image/audio parts only on user messages, so this is
// the single injection point. The trailing text part tells the model where
// the media came from. ok is false when there is nothing to deliver.
func attachmentsMessage(readMedia, userSent []llm.ContentPart) (llm.Message, bool) {
	total := len(readMedia) + len(userSent)
	if total == 0 {
		return llm.Message{}, false
	}
	parts := make([]llm.ContentPart, 0, total+1)
	parts = append(parts, readMedia...)
	parts = append(parts, userSent...)
	var notes []string
	if len(readMedia) > 0 {
		notes = append(notes, fmt.Sprintf("%d file(s) you loaded with read_media", len(readMedia)))
	}
	if len(userSent) > 0 {
		notes = append(notes, fmt.Sprintf("%d attachment(s) sent by the user", len(userSent)))
	}
	label := strings.Join(notes, "; ")
	parts = append(parts, llm.ContentPart{
		Type: llm.ContentPartTypeText,
		Text: "Above are the attached media file(s): " + label + ".",
	})
	return llm.Message{
		Role:         llm.RoleUser,
		Content:      "[attached: " + label + "]",
		ContentParts: parts,
	}, true
}

// toolLoopOutcome reports how a turn's tool-execution loop ended.
type toolLoopOutcome struct {
	allMsgs      []llm.Message     // updated slice (compact_history may have replaced it)
	cancelled    bool              // a guard returned a cancel decision
	cancelReason string            // reason text for the cancellation reminder
	pendingMedia []llm.ContentPart // media loaded by read_media, injected as a user message after the loop
}

// toolLoopState is the mutable state one tool-execution loop threads through
// its handlers: the working message slice (which compact_history replaces
// wholesale) and the media parts read_media collects for post-loop injection.
type toolLoopState struct {
	allMsgs      []llm.Message
	pendingMedia []llm.ContentPart
}

// turnScopedHandlers builds the ToolHandlers that exist per tool loop rather
// than in the shared NewHandlers map, because they need state beyond
// ToolConfig: compact_history rewrites the conversation itself,
// shell_interactive borrows the front-end's TTY runner, and read_media
// collects media parts for the post-loop user message. They close over st, so
// the trio is rebuilt for each executeToolCalls invocation.
func turnScopedHandlers(cfg TurnConfig, sess *Session, st *toolLoopState) map[string]ToolHandler {
	return map[string]ToolHandler{
		"compact_history": funcHandler{name: "compact_history", fn: func(_ context.Context, _ string, args json.RawMessage, _ ToolConfig) (string, error) {
			out, newMsgs := handleCompactHistory(string(args), cfg.Store, sess, st.allMsgs, cfg.Log)
			st.allMsgs = newMsgs
			return out, nil
		}},
		"shell_interactive": funcHandler{name: "shell_interactive", fn: func(ctx context.Context, _ string, args json.RawMessage, _ ToolConfig) (string, error) {
			if cfg.ShellInteractive == nil {
				return "error: interactive TTY not available", nil
			}
			return cfg.ShellInteractive(ctx, ParseBashArgs(string(args)), cfg.WorkDir), nil
		}},
		"read_media": funcHandler{name: "read_media", fn: func(_ context.Context, _ string, args json.RawMessage, _ ToolConfig) (string, error) {
			out, part := handleReadMedia(string(args), cfg.WorkDir)
			if part.Type != "" {
				st.pendingMedia = append(st.pendingMedia, part)
			}
			return out, nil
		}},
		"spawn_agent": funcHandler{name: "spawn_agent", fn: func(ctx context.Context, _ string, args json.RawMessage, _ ToolConfig) (string, error) {
			if cfg.Spawn == nil {
				return "error: subagent spawning is not available in this runtime", nil
			}
			var a struct {
				Task    string `json:"task"`
				Agent   string `json:"agent"`
				Workdir string `json:"workdir"`
			}
			if err := json.Unmarshal(args, &a); err != nil {
				return "error: invalid spawn_agent arguments: " + err.Error(), nil
			}
			if strings.TrimSpace(a.Task) == "" {
				return "error: spawn_agent requires a non-empty task", nil
			}
			id, err := cfg.Spawn(ctx, SpawnRequest{Task: a.Task, Agent: a.Agent, WorkDir: a.Workdir})
			if err != nil {
				return "error: spawn failed: " + err.Error(), nil
			}
			return "spawned subagent " + id + "; its result will arrive automatically when it finishes. Do not poll in a tight loop.", nil
		}},
		"list_agents": funcHandler{name: "list_agents", fn: func(_ context.Context, _ string, _ json.RawMessage, _ ToolConfig) (string, error) {
			var snap []AgentSnapshot
			if cfg.ListAgents != nil {
				snap = cfg.ListAgents()
			}
			if snap == nil {
				snap = []AgentSnapshot{}
			}
			b, err := json.Marshal(snap)
			if err != nil {
				return "error: " + err.Error(), nil
			}
			return string(b), nil
		}},
	}
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
	st := &toolLoopState{allMsgs: allMsgs}
	turnScoped := turnScopedHandlers(cfg, sess, st)
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
		var res toolResult
		handled := true
		if hookErr != nil {
			res = errResult(fmt.Sprintf("Tool-call hook failed (the on_tool_call hook script itself errored, not the user): %v. Do not retry the same call without adjusting your approach.", hookErr))
		} else if decision == guardCancel {
			if hookReason == "" {
				hookReason = "user cancelled"
			}
			cancelled = true
			cancelReason = hookReason
			res = errResult(fmt.Sprintf("USER CANCELLED the turn before this %s call ran. Reason: %s. Subsequent tool calls in this turn were not executed.", tc.Name, hookReason))
		} else if decision == guardAsk {
			if hookReason == "" {
				hookReason = "guard requested approval"
			}
			emitApprovalRequest(sess, tc.Name, tc.RawArgs, hookReason)
			approved := false
			if cfg.Approve != nil {
				// Approve blocks the turn goroutine until the host answers.
				approved = cfg.Approve(ctx, ApprovalRequest{
					Tool: tc.Name, RawArgs: tc.RawArgs, Reason: hookReason,
					Agent: cfg.Personality.Name,
				})
				// A false answer caused by ctx cancellation is not a user
				// verdict: end the turn with the typed context error before
				// emitting any decision event or fabricating a denial message
				// — mirroring the loop-top ctx check.
				if !approved && ctx.Err() != nil {
					return toolLoopOutcome{}, ctx.Err()
				}
				verdict := "deny"
				if approved {
					verdict = "allow"
				}
				emitApprovalDecision(sess, tc.Name, verdict)
			} else {
				emitApprovalDecision(sess, tc.Name, "deny (no approver)")
			}
			if !approved {
				reason := hookReason
				if cfg.Approve == nil {
					reason = "approval required but no approver is available in this front-end"
				}
				res = errResult(fmt.Sprintf("USER DENIED this %s tool call. Reason: %s. Treat this as the user explicitly disapproving this action — do NOT retry the same call. Acknowledge the denial, ask what they want instead, or pick a different approach.", tc.Name, reason))
			} else {
				// Approved: run schema validation before dispatch, same as the
				// normal (allow) path. The else-if chain doesn't reach the
				// validation branch for ask, so we handle it inline here.
				res, handled = validateCall(toolSchemas, tc)
			}
		} else if decision == guardBlock {
			if hookReason == "" {
				hookReason = "no reason given"
			}
			res = errResult(fmt.Sprintf("USER DENIED this %s tool call. Reason: %s. Treat this as the user explicitly disapproving this action — do NOT retry the same call. Acknowledge the denial, ask what they want instead, or pick a different approach.", tc.Name, hookReason))
		} else {
			res, handled = validateCall(toolSchemas, tc)
		}
		// If a hook blocked the call or validation failed, res already carries
		// the typed reason and we skip dispatch. Otherwise resolve a handler —
		// turn-scoped first, then the prefixed MCP and custom dispatchers, then
		// the shared built-ins (custom before built-ins so a config-declared
		// tool name always wins) — and run it through the single execute path.
		if !handled {
			var handler ToolHandler
			if h, ok := turnScoped[tc.Name]; ok {
				handler = h
			} else if cfg.MCPToolNames[tc.Name] {
				res = dispatchMCPTool(ctx, cfg.MCPTool, tc.Name, tc.RawArgs)
			} else if cfg.CustomToolNames[tc.Name] {
				res = dispatchCustomTool(ctx, cfg.CustomTool, tc.Name, tc.RawArgs)
			} else if h, ok := cfg.Handlers[tc.Name]; ok {
				handler = h
			} else {
				res = errResult(fmt.Sprintf("error: unknown tool %q", tc.Name))
			}
			if handler != nil {
				toolCfg := ToolConfig{
					Store:    cfg.Store,
					WorkDir:  cfg.WorkDir,
					AllMsgs:  st.allMsgs,
					SessMsgs: sess.messages,
				}
				out, herr := handler.Execute(ctx, tc.ID, json.RawMessage([]byte(tc.RawArgs)), toolCfg)
				res = classifyHandlerOutput(out)
				if herr != nil {
					// Most handlers encode failures in their output string and
					// return a nil error; a non-nil error is a genuine handler
					// fault (e.g. bash_bg failing to spawn). Log it, and if the
					// handler left no output, surface the error to the model as a
					// tool error rather than emitting an empty result.
					cfg.Log.Warn("tool handler error", "tool", tc.Name, "error", herr)
					if out == "" {
						res = errResult("error: " + herr.Error())
					}
				}
			}
		}

		emitToolResult(sess, tc.ID, tc.Name, res.output, res.isError)
		// Prepend the tool_call_id so the model has a stable handle it
		// can pass to prune_tool_result. Without this the id only lives
		// in structured metadata, which the model cannot reliably echo.
		content := fmt.Sprintf("[tool_call_id=%s]\n%s", tc.ID, res.output)
		toolMsg := llm.Message{
			Role:       llm.RoleTool,
			Content:    content,
			ToolCallID: tc.ID,
			Name:       tc.Name,
		}
		st.allMsgs = append(st.allMsgs, toolMsg)
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
				st.allMsgs = append(st.allMsgs, stub)
				sess.append(stub)
			}
			return toolLoopOutcome{allMsgs: st.allMsgs, cancelled: true, cancelReason: cancelReason}, nil
		}
	}

	if ctx.Err() != nil {
		return toolLoopOutcome{}, ctx.Err()
	}
	return toolLoopOutcome{allMsgs: st.allMsgs, pendingMedia: st.pendingMedia}, nil
}

// validateCall checks tc's arguments against the tool's schema (when one is
// registered). handled is true only when validation failed, in which case res
// carries the error result to send back to the model; otherwise the call
// should proceed to dispatch (handled false, res zero).
func validateCall(toolSchemas map[string]map[string]any, tc llm.ToolCall) (res toolResult, handled bool) {
	schema, ok := toolSchemas[tc.Name]
	if !ok {
		return toolResult{}, false
	}
	if err := validateToolArgs(schema, json.RawMessage([]byte(tc.RawArgs))); err != nil {
		return errResult(fmt.Sprintf("error: invalid tool arguments: %v", err)), true
	}
	return toolResult{}, false
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
