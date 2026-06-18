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
	"github.com/weatherjean/shell3/internal/paths"
	"github.com/weatherjean/shell3/internal/runs"
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
// traffic to .shell3_project/last_error.json under cfg.WorkDir, then records the
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
			p := paths.LastErrorPath(cfg.WorkDir)
			if werr := os.MkdirAll(filepath.Dir(p), 0o755); werr != nil {
				dumpErr = werr
			} else if werr := os.WriteFile(p, data, 0644); werr != nil {
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

	// Host-enforced auto-compaction runs BEFORE the new user message is
	// appended and allMsgs is built, so the turn proceeds against the compacted
	// history. It is best-effort and never blocks or fails the turn (see
	// maybeCompact): on any error it leaves history untouched.
	maybeCompact(ctx, cfg, sess)

	// A purely inbox-seeded turn (RunQueued → empty prompt, no parts) has an
	// empty initiating message; the queued text arrives via the inbox-drain
	// reminder below. Don't persist an empty, part-less user message — it would
	// replay as an empty user turn (rejected by real providers) on later turns.
	inboxSeeded := userMsg.Content == "" && len(userMsg.ContentParts) == 0
	if !inboxSeeded {
		sess.append(userMsg)
	}

	msgs := sess.messages

	allMsgs := make([]llm.Message, 0, len(msgs)+1)
	allMsgs = append(allMsgs, llm.Message{Role: llm.RoleSystem, Content: cfg.Personality.SystemPrompt})
	allMsgs = append(allMsgs, msgs...)

	// Inject standing reminders (host Environment/Delegation context) so they
	// sit right after the system prompt every turn. These are set by
	// SetStandingReminders and regenerated on resume — not persisted.
	for _, r := range sess.standingReminders {
		allMsgs = injectReminder(allMsgs, r)
	}

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
	reminder := interjectReminder(texts)
	if reminder != "" {
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

	// Skip a wake/inbox-seeded turn that delivers nothing to the provider: an
	// empty initiating message, plus a drained inbox whose items were all
	// whitespace-only (no reminder text) and carried no media parts. allMsgs
	// would otherwise be just [system] with no prior history and no user
	// message — a system-only request a strict provider may reject. End the turn
	// cleanly (turn_done, no usage, no stream call) instead.
	if inboxSeeded && reminder == "" && len(userParts) == 0 && len(msgs) == 0 {
		terminalEmit = func() { emitTurnDone(sess, 0, 0, 0) }
		return
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
		// get truncated by models when echoed back, breaking id-based tool-result
		// addressing (e.g. the /prune slash command); a bare integer has no
		// separator to chop at. The provider pairs ids by string match between
		// assistant.tool_calls[i].id and tool.tool_call_id, so the rewrite is
		// transparent on the wire.
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
	allMsgs      []llm.Message     // updated slice
	pendingMedia []llm.ContentPart // media loaded by read_media, injected as a user message after the loop
}

// toolLoopState is the mutable state one tool-execution loop threads through
// its handlers: the working message slice and the media parts read_media
// collects for post-loop injection.
type toolLoopState struct {
	allMsgs      []llm.Message
	pendingMedia []llm.ContentPart
}

// turnScopedHandlers builds the ToolHandlers that exist per tool loop rather
// than in the shared NewHandlers map, because they need state beyond
// ToolConfig: shell_interactive borrows the front-end's TTY runner, and
// read_media collects media parts for the post-loop user message. They close
// over st, so they are rebuilt for each executeToolCalls invocation.
func turnScopedHandlers(cfg TurnConfig, st *toolLoopState) map[string]ToolHandler {
	return map[string]ToolHandler{
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
	}
}

// executeToolCalls runs the assistant's tool calls in order, emitting
// tool_call/tool_result events and appending each tool message to both allMsgs
// and the session. It returns the updated allMsgs.
//
//   - a non-nil error means the context was cancelled mid-loop; the caller
//     emits an error terminal event and ends the turn.
//   - otherwise the loop completed normally; outcome.allMsgs carries the
//     updated message slice for the next round.
func executeToolCalls(ctx context.Context, cfg TurnConfig, sess *Session, toolCalls []llm.ToolCall, toolSchemas map[string]map[string]any, allMsgs []llm.Message) (toolLoopOutcome, error) {
	st := &toolLoopState{allMsgs: allMsgs}
	turnScoped := turnScopedHandlers(cfg, st)
	for i, tc := range toolCalls {
		if ctx.Err() != nil {
			// Cancelled mid-loop. The assistant message carrying these tool_calls
			// is already persisted, and OpenAI-compatible APIs require a tool
			// result for every tool_call id — a gap makes the NEXT request 400
			// ("tool call result does not follow tool call"). Backfill a synthetic
			// cancelled result for this and every remaining call so the session
			// stays replayable, then surface the cancellation.
			for _, rem := range toolCalls[i:] {
				appendToolResult(sess, st, rem, errResult("error: tool call cancelled"))
			}
			return toolLoopOutcome{allMsgs: st.allMsgs, pendingMedia: st.pendingMedia}, ctx.Err()
		}

		emitToolCall(sess, tc.ID, tc.Name, tc.RawArgs)
		// Every tool call dispatches directly: there is no guard engine or
		// approval flow. The only bash safety surface is shell3.wrap_bash,
		// applied inside the bash/bash_bg handlers (see ToolConfig.WrapBash),
		// not here.
		res, handled := validateCall(toolSchemas, tc)
		// If validation failed, res already carries the typed reason and we skip
		// dispatch. Otherwise resolve a handler —
		// turn-scoped first, then the custom dispatchers, then the shared
		// built-ins (custom before built-ins so a config-declared tool name
		// always wins) — and run it through the single execute path.
		if !handled {
			var handler ToolHandler
			if h, ok := turnScoped[tc.Name]; ok {
				handler = h
			} else if cfg.CustomToolNames[tc.Name] {
				res = dispatchCustomTool(ctx, cfg, tc.Name, tc.RawArgs)
			} else if h, ok := cfg.Handlers[tc.Name]; ok {
				handler = h
			} else if msg, ok := cfg.StubTools[tc.Name]; ok {
				res = okResult(msg) // hallucinated tool: return its redirect nudge
			} else {
				res = errResult(fmt.Sprintf("error: unknown tool %q", tc.Name))
			}
			if handler != nil {
				toolCfg := ToolConfig{
					Store:      cfg.Store,
					RunsDir:    cfg.RunsDir,
					WorkDir:    cfg.WorkDir,
					WrapBash:   cfg.WrapBash,
					BashSafety: cfg.BashSafety,
					Asker:      cfg.Asker,
					AllMsgs:    st.allMsgs,
					SessMsgs:   sess.messages,
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

		appendToolResult(sess, st, tc, res)
	}

	if ctx.Err() != nil {
		return toolLoopOutcome{allMsgs: st.allMsgs, pendingMedia: st.pendingMedia}, ctx.Err()
	}
	return toolLoopOutcome{allMsgs: st.allMsgs, pendingMedia: st.pendingMedia}, nil
}

// appendToolResult emits the tool_result event and appends the tool message to
// both the in-flight slice and the persisted session. Every tool_call must get
// exactly one of these (OpenAI requires strict tool_call/tool_result pairing),
// so it is the single append site for both the normal and cancelled paths.
func appendToolResult(sess *Session, st *toolLoopState, tc llm.ToolCall, res toolResult) {
	emitToolResult(sess, tc.ID, tc.Name, res.output, res.isError)
	// Prepend the tool_call_id so there is a stable handle the user can pass to
	// the /prune slash command. Without this the id only lives in structured
	// metadata, which is not visible in the rendered result.
	content := fmt.Sprintf("[tool_call_id=%s]\n%s", tc.ID, res.output)
	toolMsg := llm.Message{
		Role:       llm.RoleTool,
		Content:    content,
		ToolCallID: tc.ID,
		Name:       tc.Name,
	}
	st.allMsgs = append(st.allMsgs, toolMsg)
	sess.append(toolMsg)
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
// and emitting per-token chat.Events on the session sink.
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

// compactionFloor is the minimum number of session messages required before
// auto-compaction will run. Below this there is too little history for a summary
// to free meaningful context, so compacting only adds an LLM round-trip and a
// boilerplate summary message for no benefit.
const compactionFloor = 8

// compactionInstruction is the system prompt for the single quiet LLM call that
// produces the auto-compaction summary. It asks for a thorough narrative the
// continuation can resume from. Pointer lists are folded into the narrative
// here, so the auto path keeps CompactSummary's optional list fields empty.
const compactionInstruction = "You are compacting a long coding-assistant conversation to free context. " +
	"Write a thorough narrative summary of the conversation so far that a fresh continuation could resume from with no other context. " +
	"Cover: the user's goal and any decisions made; code written and files created or modified (with paths); commands run and their outcomes; errors encountered and how they were resolved; references worth keeping (session ids, commit hashes, URLs); and any confirmed open next steps. " +
	"Be comprehensive but do not invent detail. Output ONLY the summary prose — no preamble, no tool calls."

// maybeCompact runs host-enforced auto-compaction before a turn when the prior
// turn's prompt token count has reached the model's compact_at threshold. It is
// strictly best-effort: it must NEVER block or fail the user's turn. On any
// problem — disabled threshold, too little history, an LLM error, an empty
// summary, anything — it logs (when warranted) and returns WITHOUT compacting,
// so the turn proceeds normally on the un-compacted history.
//
// lastPromptTokens is 0 on the first turn, so the first turn never compacts.
// After a successful compaction it is reset to the (small) estimated size of the
// rewritten history so the threshold is not immediately re-tripped next turn.
func maybeCompact(ctx context.Context, cfg TurnConfig, sess *Session) {
	if cfg.CompactAt <= 0 || sess.lastPromptTokens < cfg.CompactAt {
		return
	}
	if len(sess.messages) < compactionFloor {
		return
	}

	// One quiet LLM call: a compaction system prompt over the current history.
	// We accumulate text WITHOUT emitting any Token/assistant events — the user
	// should not see the summary stream as if it were a turn response.
	compactMsgs := make([]llm.Message, 0, len(sess.messages)+1)
	compactMsgs = append(compactMsgs, llm.Message{Role: llm.RoleSystem, Content: compactionInstruction})
	compactMsgs = append(compactMsgs, sess.messages...)

	summary, err := streamQuiet(ctx, cfg.LLM, compactMsgs)
	if err != nil {
		cfg.Log.Warn("auto-compaction LLM call failed; proceeding on un-compacted history", "error", err)
		return
	}
	if strings.TrimSpace(summary) == "" {
		cfg.Log.Warn("auto-compaction produced an empty summary; proceeding on un-compacted history")
		return
	}

	// Rebuild history from the summary. compactInto rewrites sess.messages in
	// place (continuation + trigger assistant message) and rolls the store
	// session. allMsgs here is the full system+history slice the rewrite peels
	// the system prompt off of; RunTurn rebuilds its own allMsgs afterward.
	allMsgs := make([]llm.Message, 0, len(sess.messages)+1)
	allMsgs = append(allMsgs, llm.Message{Role: llm.RoleSystem, Content: cfg.Personality.SystemPrompt})
	allMsgs = append(allMsgs, sess.messages...)
	prevTokens := sess.lastPromptTokens
	compactInto(CompactSummary{Summary: summary}, cfg.Store, sess, allMsgs, cfg.Log, cfg.WorkDir, cfg.ConfigPath)

	// Reset the token gauge to the rewritten history's (small) estimate so the
	// next turn does not immediately re-trip the threshold before a real usage
	// count from the provider lands.
	sess.lastPromptTokens = estimatePromptTokens(sess.messages)

	emitCompacted(sess, prevTokens)
}

// streamQuiet calls the LLM once and returns only the accumulated assistant
// text, emitting NO chat.Events. It is the non-emitting sibling of streamOnce,
// used by maybeCompact so the auto-compaction round-trip is invisible to the
// user/UI. Tool calls and reasoning are ignored; usage is discarded.
func streamQuiet(ctx context.Context, client LLMClient, msgs []llm.Message) (string, error) {
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	var sb strings.Builder
	err := client.Stream(ctx, msgs, nil, func(ev llm.StreamEvent) {
		if ev.TextDelta != "" {
			sb.WriteString(ev.TextDelta)
		}
	})
	if ctx.Err() != nil {
		return sb.String(), ctx.Err()
	}
	return sb.String(), err
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

// saveHistory persists new messages to the runs store after a turn. Append
// failures are logged but not fatal — history is best-effort, but a silent
// drop would hide real faults (a full disk), so they surface via lg.
//
// On a compacting turn, maybeCompact runs before the user message is appended
// and resets sess.messages to a short continuation (2 messages) while
// sess.persistedLen is set to that length. This function uses persistedLen as
// the high-water mark so it always flushes exactly the new messages appended
// during this turn regardless of whether compaction ran.
func saveHistory(st *runs.Store, lg applog.Logger, sess *Session, sessionID string) {
	if st == nil {
		return
	}
	if sess.persistedLen > len(sess.messages) {
		// Shouldn't happen, but guard against it.
		return
	}
	flushMessages(st, lg, sessionID, sess.messages[sess.persistedLen:])
	sess.persistedLen = len(sess.messages)
}

// flushMessages appends each message in msgs to the runs store (one JSONL line
// per message, append-only). Best-effort: write failures are logged, not fatal.
// Shared by saveHistory (end of turn) and compactInto (flushing the incoming
// compacted session).
func flushMessages(st *runs.Store, lg applog.Logger, sessionID string, msgs []llm.Message) {
	for _, m := range msgs {
		if err := st.AppendMessage(sessionID, m); err != nil {
			lg.Warn("append message failed", "session_id", sessionID, "error", err)
		}
	}
}
