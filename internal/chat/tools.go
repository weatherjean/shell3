package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/bgjobs"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/runs"
)

// ErrHostToolNotFound is returned by a HostTool dispatcher when it does not
// recognize the called tool name, signaling dispatchCustomTool to fall through
// to the command-template (ResolveCustomTool) path. Any OTHER error from a
// HostTool is a real failure and is surfaced as-is.
var ErrHostToolNotFound = errors.New("host tool: name not handled")

// toolResult is the typed outcome of one tool call: the text recorded as the
// tool message plus whether it represents a failure. Every dispatch path in
// executeToolCalls produces one, so error-ness is carried as data instead of
// being re-derived by sniffing prefixes off the output string.
type toolResult struct {
	output  string
	isError bool
}

func okResult(out string) toolResult  { return toolResult{output: out} }
func errResult(out string) toolResult { return toolResult{output: out, isError: true} }

// classifyHandlerOutput types a built-in handler's output string. Handlers
// report in-band failures to the model as "error: …" strings (so the text and
// the flag can never disagree); this is the single place that convention is
// interpreted. Hook, validation, and dispatcher failures never pass
// through here — they construct typed errResults directly.
func classifyHandlerOutput(out string) toolResult {
	if strings.HasPrefix(out, "error:") {
		return errResult(out)
	}
	return okResult(out)
}

// dispatchCustomTool resolves a custom-tool call to its bash command + env and
// runs it. Foreground tools block and return the command's output (a non-zero
// exit is surfaced as an error result, "exited N"). Background tools dispatch
// via bgjobs (sink-reported) and return a pointer to the spawned job. The
// resolved command is the trusted author template, so it deliberately BYPASSES
// wrap_bash — the model supplies only env values, never the command string.
func dispatchCustomTool(ctx context.Context, cfg TurnConfig, name, rawArgs string) toolResult {
	// Host-registered Go tools (pkg/shell3.RegisterHostTool) return a result
	// string directly, so they dispatch here without the resolve-and-exec path.
	if cfg.HostTool != nil {
		out, err := cfg.HostTool(ctx, name, rawArgs)
		switch {
		case err == nil:
			return classifyHandlerOutput(out)
		case !errors.Is(err, ErrHostToolNotFound):
			// A real host-tool failure — surface it.
			return errResult("error: " + err.Error())
		}
		// errors.Is(err, ErrHostToolNotFound): this dispatcher doesn't own the
		// name — fall through to the command-template path below.
	}
	if cfg.ResolveCustomTool == nil {
		return errResult(fmt.Sprintf("error: unknown tool %q", name))
	}
	rt, err := cfg.ResolveCustomTool(name, rawArgs)
	if err != nil {
		return errResult("error: " + err.Error())
	}
	if rt.Background {
		if cfg.RunsDir == "" {
			return errResult("error: background tools require a runs directory")
		}
		job, err := bgjobs.Start(cfg.RunsDir, []string{"bash", "-c", rt.Command}, rt.Command, cfg.WorkDir, rt.Env)
		if err != nil {
			return errResult("error: " + err.Error())
		}
		return okResult(fmt.Sprintf("started background tool %s\npid: %d\nlog: %s\n", job.ID, job.PID, job.Log))
	}
	timeout := time.Duration(DefaultBashTimeoutSeconds) * time.Second
	if rt.Timeout > 0 {
		t := rt.Timeout
		if t > MaxBashTimeoutSeconds {
			t = MaxBashTimeoutSeconds
		}
		timeout = time.Duration(t) * time.Second
	}
	out, code := runBashCapture(ctx, []string{"bash", "-c", rt.Command}, cfg.WorkDir, rt.Env, timeout)
	if code != 0 {
		return errResult(fmt.Sprintf("error: command exited %d\n%s", code, out))
	}
	return okResult(out)
}

// CompactSummary is the structured product of one compaction: a narrative
// summary plus optional pointer lists. The host-driven auto-compaction path
// (maybeCompact) fills only Summary from a single quiet LLM call and leaves the
// pointer lists empty.
type CompactSummary struct {
	Summary             string
	ImportantFiles      []string
	ImportantReferences []string
	Skills              []string
	NextSteps           []string
}

// compactInto replaces the conversation history with a structured summary. It
// ends the current runs session and starts a new one so the compact boundary
// is visible in history. Both sess.messages and allMsgs are rebuilt in place;
// the summary is saved to history before the session rolls. Callers are
// responsible for validating that args.Summary is non-empty.
func compactInto(args CompactSummary, st *runs.Store, sess *Session, allMsgs []llm.Message, lg applog.Logger, workDir, configPath string) (newAllMsgs []llm.Message) {
	prevSessionID := sess.id

	// Roll the runs session so compact boundary is visible in history.
	if st != nil {
		// Flush only the unsaved tail of the outgoing session. Messages
		// 0..persistedLen-1 were already written by prior saveHistory calls;
		// re-flushing the full slice would duplicate those lines in the
		// append-only JSONL file. A guard mirrors saveHistory's own guard.
		if sess.persistedLen <= len(sess.messages) {
			flushMessages(st, lg, prevSessionID, sess.messages[sess.persistedLen:])
		}
		if err := st.EndSession(prevSessionID); err != nil {
			lg.Warn("end session failed during compact", "session_id", prevSessionID, "error", err)
		}
		newID, err := st.NewSession(runs.Meta{Workdir: workDir, ConfigPath: configPath})
		if err != nil {
			lg.Warn("start session failed during compact", "error", err)
		} else {
			sess.id = newID
		}
	}

	// Build the continuation message injected at the top of the new history.
	var b strings.Builder
	fmt.Fprintf(&b, "<system-reminder>\nContinuation of session %s. History compacted.\nPrior session messages are in the runs directory (use the `history` skill, or read .shell3_project/runs/%s/messages.jsonl directly).\n</system-reminder>\n\n", prevSessionID, prevSessionID)
	fmt.Fprintf(&b, "<compact-summary>\n%s\n</compact-summary>", args.Summary)
	if len(args.ImportantFiles) > 0 {
		b.WriteString("\n\n<important-files>\n")
		for _, f := range args.ImportantFiles {
			fmt.Fprintf(&b, "- %s\n", f)
		}
		b.WriteString("</important-files>")
	}
	if len(args.ImportantReferences) > 0 {
		b.WriteString("\n\n<important-references>\n")
		for _, r := range args.ImportantReferences {
			fmt.Fprintf(&b, "- %s\n", r)
		}
		b.WriteString("</important-references>")
	}
	if len(args.Skills) > 0 {
		b.WriteString("\n\n<skills-to-reread>\n")
		for _, s := range args.Skills {
			fmt.Fprintf(&b, "- %s\n", s)
		}
		b.WriteString("</skills-to-reread>")
	}
	if len(args.NextSteps) > 0 {
		b.WriteString("\n\n<next-steps>\n")
		for _, s := range args.NextSteps {
			fmt.Fprintf(&b, "- %s\n", s)
		}
		b.WriteString("</next-steps>")
	}

	continuationMsg := llm.Message{Role: llm.RoleUser, Content: b.String()}

	// Find the assistant message that triggered compact. It must be preserved
	// in both allMsgs and sess.messages so the tool result the caller appends
	// has a matching tool call — both in this turn AND in subsequent turns
	// that rebuild allMsgs from sess.messages.
	var triggerMsg *llm.Message
	for i := len(allMsgs) - 1; i >= 0; i-- {
		if allMsgs[i].Role == llm.RoleAssistant {
			m := allMsgs[i]
			triggerMsg = &m
			break
		}
	}

	// Build the rewritten history in a local, then publish it under msgMu: this
	// runs on the turn goroutine but replaces the slice the dashboard's
	// Messages() reader may be copying concurrently (see Session.msgMu).
	newMsgs := []llm.Message{continuationMsg}
	if triggerMsg != nil {
		newMsgs = append(newMsgs, *triggerMsg)
	}
	sess.msgMu.Lock()
	sess.messages = newMsgs
	sess.msgMu.Unlock()

	// Mirror the compacted context into the runs store under the NEW session id,
	// so a resume of this session loads the within-window compacted history
	// rather than the pre-compaction blob. flushMessages above wrote the OUTGOING
	// session; this writes the incoming one. Guard on a successful roll
	// (sess.id advanced past prevSessionID) so a failed NewSession doesn't
	// clobber the outgoing session's messages.
	if st != nil && sess.id != prevSessionID {
		flushMessages(st, lg, sess.id, newMsgs)
		// The new session's messages are now persisted; advance the high-water
		// mark so the next saveHistory doesn't re-flush them.
		sess.persistedLen = len(newMsgs)
	} else {
		// Session roll failed; the outgoing session is still active. Reset to
		// zero so saveHistory starts fresh (avoids a stale offset).
		sess.persistedLen = 0
	}

	// Rebuild allMsgs: system prompt + continuation + trigger assistant message.
	// Caller appends the tool result, completing the valid call/result pair.
	newAllMsgs = []llm.Message{allMsgs[0], continuationMsg}
	if triggerMsg != nil {
		newAllMsgs = append(newAllMsgs, *triggerMsg)
	}

	return newAllMsgs
}

// PruneByID replaces the tool result with the given id in any of the slices
// with a short stem stub. summary is a human-readable status string; ok is
// false when no tool result with that id exists in the slices, so callers
// branch on the flag instead of parsing the summary. Used by the host-side
// /prune slash command (pkg/shell3.Session.Prune); element mutations propagate
// to the caller's slices.
func PruneByID(toolCallID, stem string, slices ...[]llm.Message) (summary string, ok bool) {
	var target *llm.Message
	var name string
	for _, msgs := range slices {
		for i := range msgs {
			if msgs[i].Role == llm.RoleTool && msgs[i].ToolCallID == toolCallID {
				target = &msgs[i]
				name = msgs[i].Name
				break
			}
		}
		if target != nil {
			break
		}
	}
	if target == nil {
		return fmt.Sprintf("error: no tool result with id %q in conversation", toolCallID), false
	}

	content := target.Content
	stub := fmt.Sprintf("[%s — original was %d bytes]", stem, len(content))
	count := 0
	for _, msgs := range slices {
		for i := range msgs {
			if msgs[i].Role == llm.RoleTool && msgs[i].ToolCallID == toolCallID {
				msgs[i].Content = stub
				count++
			}
		}
	}
	if count == 0 {
		return "error: failed to update message content", false
	}
	return fmt.Sprintf("Pruned result of %s (id=%s): freed %d bytes", name, toolCallID, len(content)-len(stub)), true
}
