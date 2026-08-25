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

// headlessReminder is injected once at the start of a headless turn so the
// model understands the environment.
const headlessReminder = "<system-reminder>\nheadless mode: no interactive shell, no human available to answer questions. Decide and proceed. Destructive commands may be blocked by host policy — if a block occurs, adapt rather than retry.\n</system-reminder>"

// logStreamError dumps the failing turn's messages and last raw HTTP traffic
// to .shell3_project/last_error.json, then logs at Debug — the front-end
// already shows the error, so stderr would duplicate it.
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
				// Don't advertise a dump that wasn't written.
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

// RunTurn executes one user→assistant turn, delivering events to the sink
// synchronously; when it returns everything, terminal event included, has
// been delivered.
//
// beforeDone runs at teardown immediately before that terminal event —
// Session.Run persists history there. The ordering matters: front-ends treat
// the terminal event as "safe to mutate session state", so a read of
// sess.messages in beforeDone must finish first or it races SetMessages.
func RunTurn(ctx context.Context, cfg TurnConfig, sess *Session, userMsg llm.Message, beforeDone func()) {
	// Reset first: a turn returning via the early skip path below must not let
	// saveHistory re-add a previous turn's usage to the cumulative ledger.
	sess.turnUsage = llm.Usage{}
	// terminalEmit is emitted from the defer below, after beforeDone, so
	// persistence happens-before the signal the front-end reacts to.
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

	// Auto-compaction runs BEFORE the user message is appended, so the turn
	// proceeds against the compacted history. Best-effort: on any error it
	// leaves history untouched.
	maybeCompact(ctx, cfg, sess)

	// An inbox-seeded turn has an empty initiating message; its text arrives
	// through the drain reminder below. Persisting the empty one would replay
	// as an empty user turn, which real providers reject.
	inboxSeeded := userMsg.Content == "" && len(userMsg.ContentParts) == 0
	if !inboxSeeded {
		sess.append(userMsg)
	}

	allMsgs, toolList, toolSchemas, skip := assembleTurnContext(cfg, sess, inboxSeeded)
	if skip {
		terminalEmit = func() { emitTurnDone(sess, llm.Usage{}) }
		return
	}

	var totalUsage llm.Usage
	for {
		text, reasoning, toolCalls, usage, truncated, err := streamOnce(ctx, cfg.LLM, allMsgs, toolList, sess)
		if usage != (llm.Usage{}) {
			totalUsage = addUsage(totalUsage, usage)
			emitUsage(sess, totalUsage)
			// totalUsage's PromptTokens is "latest round wins" — right for the
			// context gauge, wrong for cost: a tool-heavy turn's later rounds
			// re-send and pay for the whole growing prompt again. The ledger
			// therefore sums each round raw rather than reusing the merge.
			sess.turnUsage.PromptTokens += usage.PromptTokens
			sess.turnUsage.CompletionTokens += usage.CompletionTokens
		}
		if usage.PromptTokens > 0 {
			sess.lastPromptTokens = usage.PromptTokens
		}
		if err != nil {
			logStreamError(cfg, allMsgs, err)
			// Capture into a fresh local so terminalEmit carries the value
			// itself and errors.Is/As survives the public boundary.
			streamErr := err
			terminalEmit = func() { emitError(sess, streamErr) }
			return
		}
		// A capped response stops mid-sentence, reported only as finish_reason
		// "length", so without this the user sees a mangled reply and no
		// reason. The notice rides the token stream AND stays in the recorded
		// message, so next round the model knows its output was cut rather
		// than something it chose to say.
		if truncated {
			emitAssistantToken(sess, truncationNotice)
			text += truncationNotice
		}
		if text != "" {
			emitAssistantMessage(sess, text)
		}

		// Replace provider ids with sequential decimal ones. Native ids like
		// "web_fetch:0" get truncated by models when echoed back, breaking
		// id-based addressing; a bare integer has no separator to chop at.
		// Pairing is by string match, so the rewrite is transparent on the wire.
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
			terminalEmit = func() { emitTurnDone(sess, u) }
			return
		}

		// toolErr, unlike the stream err above, is non-nil only on a
		// cancellation observed during the tool loop.
		outcome, toolErr := executeToolCalls(ctx, cfg, sess, toolCalls, toolSchemas, allMsgs)
		if toolErr != nil {
			turnErr := toolErr
			terminalEmit = func() { emitError(sess, turnErr) }
			return
		}
		allMsgs = outcome.allMsgs

		// A reminder due before the next round appends to the last tool
		// message in allMsgs only; sess.messages stays clean. Counting bytes
		// across all of allMsgs reflects pruning with no delta tracking.
		injectAndEmit(sess, &allMsgs, sess.reminders.check(cfg.StatusLine, estimatePromptTokens(allMsgs)), true)
		// Mid-turn: steering is interactive and delivered now; notices wait for
		// a turn boundary so a finished task never interrupts this turn.
		steerTexts, _ := sess.drainInbox(true)
		injectAndEmit(sess, &allMsgs, reminderBlock(steerReminderHeader, steerTexts), true)

		// Tool messages cannot carry media, so read_media's files are appended
		// as a synthetic user message — the only role the adapter renders
		// parts for. After the reminder block, so the reminder lands on the
		// last tool message rather than this parts-carrying one.
		if msg, ok := attachmentsMessage(outcome.pendingMedia); ok {
			allMsgs = append(allMsgs, msg)
			sess.append(msg)
		}
	}
}

// injectAndEmit adds reminder r to the outbound context and mirrors it on the
// event stream, no-op for "". appendToLast=false uses injectReminder, so the
// reminder rides the turn's user message; true appends to the trailing
// message, the mid-turn path onto the round's last tool message.
func injectAndEmit(sess *Session, allMsgs *[]llm.Message, r string, appendToLast bool) {
	if r == "" {
		return
	}
	if appendToLast {
		(*allMsgs)[len(*allMsgs)-1].Content += "\n\n" + r
	} else {
		*allMsgs = injectReminder(*allMsgs, r)
	}
	emitSystemReminder(sess, r)
}

// assembleTurnContext builds one turn's provider-bound context: system prompt
// + history + standing reminders, the tool list and schema index, then the
// turn-start inbox drain. A fresh turn is a clean boundary, so BOTH steering
// and notices are delivered, each in its own labeled block.
//
// skip=true means the turn delivers nothing: empty initiating message, a
// drained inbox of only whitespace, no history. allMsgs would be just
// [system], which a strict provider may reject, so the caller ends cleanly.
func assembleTurnContext(cfg TurnConfig, sess *Session, inboxSeeded bool) (allMsgs []llm.Message, toolList []llm.ToolDefinition, toolSchemas map[string]map[string]any, skip bool) {
	msgs := sess.messages

	// Re-rendered per turn when a refresher is wired, so context: files track
	// the disk NOW; otherwise a long-lived conversation serves the
	// session-creation snapshot until /new or a restart.
	sysPrompt := renderSystemPrompt(cfg)
	recordTurnPrompt(cfg, sess, sysPrompt, len(msgs))
	allMsgs = make([]llm.Message, 0, len(msgs)+1)
	allMsgs = append(allMsgs, llm.Message{Role: llm.RoleSystem, Content: sysPrompt})
	allMsgs = append(allMsgs, msgs...)

	// Standing reminders sit right after the system prompt every turn,
	// regenerated on resume rather than persisted. Snapshot via the accessor:
	// an agent switch may replace the slice mid-turn.
	for _, r := range sess.StandingReminders() {
		allMsgs = injectReminder(allMsgs, r)
	}

	toolList = cfg.Personality.Tools
	if cfg.Headless {
		allMsgs = injectReminder(allMsgs, headlessReminder)
	}

	toolSchemas = make(map[string]map[string]any, len(toolList))
	for _, td := range toolList {
		toolSchemas[td.Name] = td.Parameters
	}

	steerTexts, noticeTexts := sess.drainInbox(false)
	// A delivered report leaves its one-line trace BEFORE any reminder
	// injects, so reminders land on it and the reply keeps a visible cause.
	if trace := reportTrace(noticeTexts); trace != "" {
		msg := llm.Message{Role: llm.RoleUser, Content: trace}
		allMsgs = append(allMsgs, msg)
		sess.append(msg)
	}
	injectAndEmit(sess, &allMsgs, sess.reminders.check(cfg.StatusLine, sess.lastPromptTokens), false)
	steerReminder := reminderBlock(steerReminderHeader, steerTexts)
	noticeReminder := reminderBlock(noticeReminderHeader, noticeTexts)
	injectAndEmit(sess, &allMsgs, steerReminder, false)
	injectAndEmit(sess, &allMsgs, noticeReminder, false)

	skip = inboxSeeded && steerReminder == "" && noticeReminder == "" && len(msgs) == 0
	return allMsgs, toolList, toolSchemas, skip
}

// attachmentsMessage delivers the media read_media loaded this round. Tool
// messages cannot carry it and the adapter renders parts only on user
// messages, so this is the single injection point; the trailing text part says
// where the media came from. ok is false when there is nothing to deliver.
func attachmentsMessage(readMedia []llm.ContentPart) (llm.Message, bool) {
	if len(readMedia) == 0 {
		return llm.Message{}, false
	}
	parts := make([]llm.ContentPart, 0, len(readMedia)+1)
	parts = append(parts, readMedia...)
	label := fmt.Sprintf("%d file(s) you loaded with read_media", len(readMedia))
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

// toolLoopState is what one tool loop threads through its handlers and
// reports back: the working message slice and read_media's collected parts.
type toolLoopState struct {
	allMsgs      []llm.Message     // updated slice
	pendingMedia []llm.ContentPart // media loaded by read_media, injected as a user message after the loop
}

// turnScopedHandlers builds the handlers that need state beyond ToolConfig —
// read_media collects parts for the post-loop message. They close over st, so
// they are rebuilt per executeToolCalls call.
func turnScopedHandlers(cfg TurnConfig, st *toolLoopState) map[string]ToolHandler {
	return map[string]ToolHandler{
		"read_media": funcHandler{name: "read_media", fn: func(_ context.Context, _ string, args json.RawMessage, _ ToolConfig) (string, error) {
			out, part := handleReadMedia(string(args), cfg.WorkDir)
			if part.Type != "" {
				st.pendingMedia = append(st.pendingMedia, part)
			}
			return out, nil
		}},
	}
}

// executeToolCalls runs the tool calls in order, emitting events and
// appending each tool message to allMsgs and the session. A non-nil error
// means the context was cancelled mid-loop and the caller ends the turn;
// otherwise outcome.allMsgs carries the slice for the next round.
func executeToolCalls(ctx context.Context, cfg TurnConfig, sess *Session, toolCalls []llm.ToolCall, toolSchemas map[string]map[string]any, allMsgs []llm.Message) (toolLoopState, error) {
	st := &toolLoopState{allMsgs: allMsgs}
	turnScoped := turnScopedHandlers(cfg, st)
	for i, tc := range toolCalls {
		if ctx.Err() != nil {
			// Cancelled mid-loop. The assistant message is already persisted
			// and every tool_call id needs a result — a gap 400s the NEXT
			// request — so backfill a synthetic cancelled result for this and
			// every remaining call, then surface the cancellation.
			for _, rem := range toolCalls[i:] {
				appendToolResult(sess, st, rem, errResult("error: tool call cancelled"))
			}
			return *st, ctx.Err()
		}

		emitToolCall(sess, tc.ID, tc.Name, tc.RawArgs)
		res, invalid := validateCall(toolSchemas, tc)
		// On a validation failure res already carries the reason. Otherwise
		// gate, then resolve a handler: turn-scoped, then custom dispatchers,
		// then built-ins — custom first, so a declared tool name always wins.
		if !invalid {
			// The gate is the only policy surface and fires before every tool.
			// The bash family self-gates in its handlers, where rewrite and
			// runner-swap resolve; everything else is gated here by name and
			// args, pass or block only, with a nil command.
			gateMsg, gateBlocked := "", false
			if !isBashTool(tc.Name) {
				gateMsg, gateBlocked = gateNonBashTool(ctx, cfg.ToolConfig, tc.Name, tc.RawArgs)
			}
			if gateBlocked {
				res = errResult(gateMsg)
			} else {
				var handler ToolHandler
				if h, ok := turnScoped[tc.Name]; ok {
					handler = h
				} else if cfg.HostToolNames[tc.Name] {
					res = dispatchHostTool(ctx, cfg, tc.Name, tc.RawArgs)
				} else if h, ok := cfg.Handlers[tc.Name]; ok {
					handler = h
				} else {
					res = errResult(unknownToolMsg(tc.Name))
				}
				if handler != nil {
					out, herr := handler.Execute(ctx, tc.ID, json.RawMessage(tc.RawArgs), cfg.ToolConfig)
					res = classifyHandlerOutput(out)
					if herr != nil {
						// Handlers normally encode failure in their output, so a
						// non-nil error is a genuine fault. Log it, and with no
						// output surface it to the model rather than an empty
						// result.
						cfg.Log.Warn("tool handler error", "tool", tc.Name, "error", herr)
						if out == "" {
							res = errResult("error: " + herr.Error())
						}
					}
				}
			}
		}

		if cfg.RunToolResult != nil {
			res.output = cfg.RunToolResult(ctx, tc.Name, tc.RawArgs, res.output)
		}
		appendToolResult(sess, st, tc, res)
	}

	if ctx.Err() != nil {
		return *st, ctx.Err()
	}
	return *st, nil
}

// appendToolResult emits the tool_result event and appends the tool message
// to both the in-flight slice and the session. Every tool_call needs exactly
// one, so this is the single append site for the normal and cancelled paths.
func appendToolResult(sess *Session, st *toolLoopState, tc llm.ToolCall, res toolResult) {
	emitToolResult(sess, tc.ID, tc.Name, res.output, res.isError)
	// Prepend the tool_call_id: without it the handle lives only in structured
	// metadata, invisible in the rendered transcript.
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

// validateCall checks tc's arguments against the tool's schema when one is
// registered. invalid means res carries the error result for the model;
// otherwise the call proceeds to dispatch and res is zero.
func validateCall(toolSchemas map[string]map[string]any, tc llm.ToolCall) (res toolResult, invalid bool) {
	schema, ok := toolSchemas[tc.Name]
	if !ok {
		return toolResult{}, false
	}
	if err := validateToolArgs(schema, json.RawMessage(tc.RawArgs)); err != nil {
		return errResult(fmt.Sprintf("error: invalid tool arguments: %v", err)), true
	}
	return toolResult{}, false
}

// truncationNotice is appended to a reply the provider cut at the output token
// cap. Raising the model's max_tokens is the fix, so the notice names it.
const truncationNotice = "\n\n⚠️ [output cut off — hit the model's max_tokens limit]"

// IsTruncatedReply reports the truncation notice in assistant text. A
// front-end rendering for a human can ignore it — the notice speaks for
// itself — but one consuming the text PROGRAMMATICALLY must check: the notice
// rides the reply, not an error channel, so a cut-off reply is otherwise
// indistinguishable from a complete one.
func IsTruncatedReply(text string) bool {
	return strings.Contains(text, truncationNotice)
}

// streamOnce calls the LLM once, collecting text/reasoning/tool-calls/usage
// and emitting per-token chat.Events on the session sink.
func streamOnce(ctx context.Context, client LLMClient, msgs []llm.Message, tools []llm.ToolDefinition, sess *Session) (text, reasoning string, toolCalls []llm.ToolCall, usage llm.Usage, truncated bool, err error) {
	if ctx.Err() != nil {
		return "", "", nil, llm.Usage{}, false, ctx.Err()
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
		if ev.Truncated {
			truncated = true
		}
	})
	if ctx.Err() != nil {
		return sb.String(), rb.String(), toolCalls, usage, truncated, ctx.Err()
	}
	return sb.String(), rb.String(), toolCalls, usage, truncated, streamErr
}

// addUsage accumulates usage across the rounds one turn can take. Each round
// re-sends the full context, so prompt tokens are not additive and only the
// latest count is meaningful; completion tokens genuinely are.
func addUsage(a, b llm.Usage) llm.Usage {
	completion := a.CompletionTokens + b.CompletionTokens
	// A follow-up round may omit the prompt count; keep the last known one
	// rather than zeroing the round's reported prompt and total.
	prompt, cached := b.PromptTokens, b.CachedTokens
	if prompt == 0 {
		prompt, cached = a.PromptTokens, a.CachedTokens
	}
	return llm.Usage{
		PromptTokens:     prompt,
		CompletionTokens: completion,
		TotalTokens:      prompt + completion,
		CachedTokens:     cached,
	}
}

// saveHistory persists a turn's new messages. Append failures are logged, not
// fatal — history is best-effort, but a silent drop would hide a full disk.
//
// It flushes from persistedLen, the high-water mark, so a compacting turn —
// where maybeCompact already reset sess.messages and that mark together —
// still flushes exactly this turn's new messages.
func saveHistory(st *runs.Store, lg applog.Logger, sess *Session, sessionID string) {
	if st == nil {
		return
	}
	if sess.persistedLen > len(sess.messages) {
		return
	}
	sess.persistedLen += flushMessages(st, lg, sessionID, sess.messages[sess.persistedLen:])
	// Persist the provider-reported prompt count so a resume restores the
	// accurate gauge rather than the chars/4 estimate. Best-effort, and a
	// no-op when unchanged.
	if sess.lastPromptTokens > 0 {
		if err := st.SetLastPromptTokens(sessionID, sess.lastPromptTokens); err != nil {
			lg.Warn("persist prompt tokens failed", "session_id", sessionID, "error", err)
		}
	}
	// Accumulate this turn's total usage onto the store's cumulative ledger
	// (the gauge above and the ledger here update from the same totalUsage
	// source, so they can never drift apart). Guarded on non-zero like the
	// gauge write: a turn that made no LLM call (pure inbox drain, an early
	// error before any round) has nothing to add.
	if sess.turnUsage.PromptTokens > 0 || sess.turnUsage.CompletionTokens > 0 {
		if err := st.AddUsage(sessionID, sess.turnUsage.PromptTokens, sess.turnUsage.CompletionTokens); err != nil {
			lg.Warn("persist cumulative usage failed", "session_id", sessionID, "error", err)
		}
	}
}

// flushMessages appends each message in msgs to the runs store (one row per
// message, append-only) and returns how many were persisted. Best-effort:
// a write failure is logged, not fatal — but it STOPS the flush and the count
// reflects only the contiguous persisted prefix, so the caller advances its
// high-water mark no further than what actually reached disk. Continuing past a
// failure would let the high-water mark skip an unwritten message, permanently
// losing it (and orphaning a tool_call from its result). The unwritten tail is
// retried on the next flush. Shared by saveHistory and compactInto.
func flushMessages(st *runs.Store, lg applog.Logger, sessionID string, msgs []llm.Message) int {
	for i, m := range msgs {
		if err := st.AppendMessage(sessionID, m); err != nil {
			lg.Warn("append message failed", "session_id", sessionID, "error", err)
			return i
		}
	}
	return len(msgs)
}

// recordTurnPrompt persists the system prompt this turn is about to run with,
// so a stored conversation records what the model was TOLD and not only what
// it said. Content-addressed and skipped when unchanged, so the steady state
// (nothing edited between turns) costs one hash lookup; see
// internal/runs/prompts.go.
//
// Best-effort by design: a failure here is logged and the turn proceeds. The
// prompt is a debugging record, and losing it must never cost the user an
// answer.
func recordTurnPrompt(cfg TurnConfig, sess *Session, sysPrompt string, seq int) {
	if cfg.Store == nil || sess == nil || sess.id == "" {
		return
	}
	if err := cfg.Store.SavePrompt(sess.id, seq, sysPrompt, time.Now()); err != nil && cfg.Log != nil {
		cfg.Log.Warn("persist system prompt failed", "session_id", sess.id, "error", err)
	}
}

// renderSystemPrompt is the ONE place a turn's system prompt is assembled:
// the persona's prompt, replaced by the refresher when one is wired (context
// files re-read from disk, a fresh timestamp), then the session's own suffix.
// Both are closures called per turn, so an edit to either source lands on the
// next turn rather than at the next restart.
func renderSystemPrompt(cfg TurnConfig) string {
	sysPrompt := cfg.Personality.SystemPrompt
	if cfg.RefreshPrompt != nil {
		if s := cfg.RefreshPrompt(); s != "" {
			sysPrompt = s
		}
	}
	if cfg.PromptSuffix != nil {
		if suffix := strings.TrimSpace(cfg.PromptSuffix()); suffix != "" {
			sysPrompt = strings.TrimRight(sysPrompt, "\n") + "\n\n" + suffix
		}
	}
	return sysPrompt
}
