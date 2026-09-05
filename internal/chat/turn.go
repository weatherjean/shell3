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
	"github.com/weatherjean/shell3/internal/strutil"
)

// headlessReminder is injected once at the start of a headless turn so the
// model understands the environment.
const headlessReminder = "<system-reminder>\nheadless mode: no interactive shell, no human available to answer questions. Decide and proceed. Destructive commands may be blocked by host policy — if a block occurs, adapt rather than retry.\n</system-reminder>"

const (
	errorDumpMaxBytes      = 2 << 20
	errorDumpMessages      = 12
	errorDumpMessageBytes  = 32 << 10
	errorDumpTrafficBytes  = 128 << 10
	errorDumpToolCalls     = 8
	errorDumpToolArgsBytes = 16 << 10
	errorDumpErrorBytes    = 16 << 10
)

// logStreamError retains a bounded, session-local provider trace and records
// its location in the project diagnostic log.
func logStreamError(cfg TurnConfig, sessionID string, msgs []llm.Message, streamErr error) {
	var reqBody, resBody []byte
	if ts, ok := cfg.LLM.(llm.TrafficInspector); ok {
		reqBody, resBody = ts.LastTraffic()
	}
	dumpPath := ""
	var dumpErr error
	if cfg.WorkDir != "" {
		if data, err := buildErrorDump(msgs, streamErr, reqBody, resBody, time.Now()); err == nil {
			p := paths.LastErrorPath(cfg.WorkDir, sessionID)
			if werr := writePrivateAtomic(p, data); werr != nil {
				// Don't advertise a dump that wasn't written.
				dumpErr = werr
			} else {
				dumpPath = p
			}
		} else {
			dumpErr = err
		}
	}
	cfg.Log.Error("stream error", streamErr, "session", sessionID, "dump", dumpPath, "dump_error", dumpErr,
		"req_bytes", len(reqBody), "res_bytes", len(resBody))
}

func buildErrorDump(msgs []llm.Message, streamErr error, reqBody, resBody []byte, now time.Time) ([]byte, error) {
	start := max(0, len(msgs)-errorDumpMessages)
	bounded := make([]llm.Message, 0, len(msgs)-start)
	for _, msg := range msgs[start:] {
		copyMsg := msg
		copyMsg.Content = strutil.Truncate(msg.Content, errorDumpMessageBytes)
		copyMsg.ReasoningContent = strutil.Truncate(msg.ReasoningContent, errorDumpMessageBytes)
		copyMsg.OperatorContent = ""
		if len(msg.ToolCalls) > errorDumpToolCalls {
			copyMsg.ToolCalls = append([]llm.ToolCall(nil), msg.ToolCalls[:errorDumpToolCalls]...)
		} else {
			copyMsg.ToolCalls = append([]llm.ToolCall(nil), msg.ToolCalls...)
		}
		for i := range copyMsg.ToolCalls {
			copyMsg.ToolCalls[i].RawArgs = strutil.Truncate(copyMsg.ToolCalls[i].RawArgs, errorDumpToolArgsBytes)
		}
		bounded = append(bounded, copyMsg)
	}
	rec := map[string]any{
		"timestamp":        now.UTC().Format(time.RFC3339),
		"error":            strutil.Truncate(streamErr.Error(), errorDumpErrorBytes),
		"messages":         bounded,
		"messages_omitted": start,
		"request_body":     strutil.Truncate(string(reqBody), errorDumpTrafficBytes),
		"response_body":    strutil.Truncate(string(resBody), errorDumpTrafficBytes),
	}
	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return nil, err
	}
	if len(data) <= errorDumpMaxBytes {
		return data, nil
	}
	return json.MarshalIndent(map[string]any{
		"timestamp": now.UTC().Format(time.RFC3339),
		"error":     strutil.Truncate(streamErr.Error(), errorDumpErrorBytes),
		"note":      "diagnostic details exceeded the size limit and were omitted",
	}, "", "  ")
}

func writePrivateAtomic(path string, data []byte) (err error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".last_error-*.tmp")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() {
		_ = f.Close()
		_ = os.Remove(tmp)
	}()
	if err := f.Chmod(0o600); err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// RunTurn executes one user→assistant turn, delivering events to the sink
// synchronously; when it returns everything, terminal event included, has
// been delivered.
//
// beforeDone runs at teardown immediately before that terminal event —
// Session.Run persists history there, so terminal observers always see the
// completed durable state.
func RunTurn(ctx context.Context, cfg TurnConfig, sess *Session, userMsg llm.Message, beforeDone func()) {
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
	inboxSeeded := userMsg.Content == ""
	if !inboxSeeded {
		if cfg.TrustedUserContext && userMsg.Role == llm.RoleUser {
			userMsg.OperatorContent = userMsg.Content
		}
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
		}
		if usage.PromptTokens > 0 {
			sess.lastPromptTokens = usage.PromptTokens
		}
		if err != nil {
			logStreamError(cfg, sess.id, allMsgs, err)
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
		injectAndEmit(sess, &allMsgs, sess.reminders.check(cfg.ModelID, cfg.ContextWindow, estimatePromptTokens(allMsgs)), true)
		// Mid-turn: steering is interactive and delivered now; host notices wait
		// for a turn boundary.
		steerTexts, _ := sess.drainInbox(true)
		injectAndEmit(sess, &allMsgs, reminderBlock(steerReminderHeader, steerTexts), true)
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
	recordSystemReminder(sess, r)
}

// assembleTurnContext builds one turn's provider-bound context: system prompt
// + history + standing reminders, the tool list and schema index, then the
// turn-start inbox drain. A fresh turn is a clean boundary, so both steering
// and host notices are delivered in separately labeled blocks.
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
	allMsgs = make([]llm.Message, 0, len(msgs)+1)
	allMsgs = append(allMsgs, llm.Message{Role: llm.RoleSystem, Content: sysPrompt})
	allMsgs = append(allMsgs, msgs...)

	// Standing reminders sit right after the system prompt every turn,
	// regenerated on resume rather than persisted. Snapshot via the accessor
	// because reload may replace the slice between turns.
	for _, r := range sess.StandingReminders() {
		allMsgs = injectReminder(allMsgs, r)
	}

	toolList = cfg.Profile.Tools
	if cfg.Headless {
		allMsgs = injectReminder(allMsgs, headlessReminder)
	}

	toolSchemas = make(map[string]map[string]any, len(toolList))
	for _, td := range toolList {
		toolSchemas[td.Name] = td.Parameters
	}

	steerTexts, noticeTexts := sess.drainInbox(false)
	// A delivered host notice leaves its one-line trace before any reminder
	// injects, so reminders land on it and the reply keeps a visible cause.
	if trace := hostNoticeTrace(noticeTexts); trace != "" {
		msg := llm.Message{Role: llm.RoleUser, Content: trace}
		allMsgs = append(allMsgs, msg)
		sess.append(msg)
	}
	injectAndEmit(sess, &allMsgs, sess.reminders.check(cfg.ModelID, cfg.ContextWindow, sess.lastPromptTokens), false)
	steerReminder := reminderBlock(steerReminderHeader, steerTexts)
	noticeReminder := reminderBlock(hostNoticeReminderHeader, noticeTexts)
	injectAndEmit(sess, &allMsgs, steerReminder, false)
	injectAndEmit(sess, &allMsgs, noticeReminder, false)

	skip = inboxSeeded && steerReminder == "" && noticeReminder == "" && len(msgs) == 0
	return allMsgs, toolList, toolSchemas, skip
}

// toolLoopState is what one tool loop threads through its handlers and
// reports back.
type toolLoopState struct {
	allMsgs []llm.Message // updated slice
}

// executeToolCalls runs the tool calls in order, emitting events and
// appending each tool message to allMsgs and the session. A non-nil error
// means the context was cancelled mid-loop and the caller ends the turn;
// otherwise outcome.allMsgs carries the slice for the next round.
func executeToolCalls(ctx context.Context, cfg TurnConfig, sess *Session, toolCalls []llm.ToolCall, toolSchemas map[string]map[string]any, allMsgs []llm.Message) (toolLoopState, error) {
	st := &toolLoopState{allMsgs: allMsgs}
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

		emitToolCall(sess, tc.Name, tc.RawArgs)
		res, invalid := validateCall(toolSchemas, tc)
		// On a validation failure res already carries the reason. Otherwise
		// resolve host tools first, then built-ins.
		if !invalid {
			var handler ToolHandler
			if cfg.HostToolNames[tc.Name] {
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
		return *st, ctx.Err()
	}
	return *st, nil
}

// appendToolResult emits the tool_result event and appends the tool message
// to both the in-flight slice and the session. Every tool_call needs exactly
// one, so this is the single append site for the normal and cancelled paths.
func appendToolResult(sess *Session, st *toolLoopState, tc llm.ToolCall, res toolResult) {
	emitToolResult(sess, tc.Name, res.output, res.isError)
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

// truncationNotice is appended when a reply hits the output token cap.
const truncationNotice = "\n\n⚠️ [output cut off — hit the model's max_tokens limit]"

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

// renderSystemPrompt appends the live per-session transport suffix to the
// configured agent prompt.
func renderSystemPrompt(cfg TurnConfig) string {
	sysPrompt := cfg.Profile.SystemPrompt
	if cfg.PromptSuffix != nil {
		if suffix := strings.TrimSpace(cfg.PromptSuffix()); suffix != "" {
			sysPrompt = strings.TrimRight(sysPrompt, "\n") + "\n\n" + suffix
		}
	}
	return sysPrompt
}
