package chat

import (
	"context"
	"fmt"
	"strings"

	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/store"
)

// guardDecision is the outcome of a tool-call guard. It is an alias for int
// (so existing int comparisons keep compiling) whose values mirror
// luacfg.Decision and must not be changed without updating that type.
type guardDecision = int

const (
	guardAllow  guardDecision = 0 // proceed with the tool call
	guardBlock  guardDecision = 1 // deny this single tool call; turn continues
	guardCancel guardDecision = 2 // abort the entire turn
	// guardAsk suspends the call pending host approval (Approve hook in TurnConfig).
	// Values mirror luacfg.Decision and must stay in sync.
	guardAsk guardDecision = 3
)

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
// interpreted. Guard, hook, validation, and dispatcher failures never pass
// through here — they construct typed errResults directly.
func classifyHandlerOutput(out string) toolResult {
	if strings.HasPrefix(out, "error:") {
		return errResult(out)
	}
	return okResult(out)
}

// dispatchCustomTool calls custom for a named custom tool. If custom is nil the
// call returns an unknown-tool error.
func dispatchCustomTool(ctx context.Context, custom func(ctx context.Context, name, argsJSON string) (string, error), name, rawArgs string) toolResult {
	if custom == nil {
		return errResult(fmt.Sprintf("error: unknown tool %q", name))
	}
	out, err := custom(ctx, name, rawArgs)
	if err != nil {
		return errResult("error: " + err.Error())
	}
	return classifyHandlerOutput(out)
}

// CompactSummary is the structured product of one compaction: a narrative
// summary plus optional pointer lists. The model-driven compact tool used to
// supply all fields; the host-driven auto-compaction path (maybeCompact) fills
// only Summary from a single quiet LLM call and leaves the lists empty.
type CompactSummary struct {
	Summary             string
	ImportantFiles      []string
	ImportantReferences []string
	Skills              []string
	NextSteps           []string
}

// compactInto replaces the conversation history with a structured summary. It
// ends the current store session and starts a new one so the compact boundary
// is visible in history. Both sess.messages and allMsgs are rebuilt in place;
// the summary is saved to history before the session rolls. Callers are
// responsible for validating that args.Summary is non-empty.
func compactInto(args CompactSummary, st *store.Store, sess *Session, allMsgs []llm.Message, lg applog.Logger) (out string, newAllMsgs []llm.Message) {
	prevSessionID := sess.id

	// Roll the store session so compact boundary is visible in history.
	if st != nil {
		// Flush current session messages before wiping — saveHistory bails early
		// after compact because prevLen > len(sess.messages), so we save here.
		flushMessages(st, lg, prevSessionID, sess.messages)
		// Save the summary itself as the final entry in the outgoing session.
		appendHistory(st, lg, prevSessionID, "tool", "compact_history: "+args.Summary)
		if err := st.EndSession(prevSessionID); err != nil {
			lg.Warn("end session failed during compact", "session_id", prevSessionID, "error", err)
		}
		newID, err := st.StartSession()
		if err != nil {
			lg.Warn("start session failed during compact", "error", err)
		} else {
			sess.id = newID
		}
	}

	// Build the continuation message injected at the top of the new history.
	var b strings.Builder
	fmt.Fprintf(&b, "<system-reminder>\nContinuation of session %d. History compacted.\nFull prior conversation available via history_get(session_id=%d).\n</system-reminder>\n\n", prevSessionID, prevSessionID)
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

	sess.messages = []llm.Message{continuationMsg}
	if triggerMsg != nil {
		sess.messages = append(sess.messages, *triggerMsg)
	}

	// Rebuild allMsgs: system prompt + continuation + trigger assistant message.
	// Caller appends the tool result, completing the valid call/result pair.
	newAllMsgs = []llm.Message{allMsgs[0], continuationMsg}
	if triggerMsg != nil {
		newAllMsgs = append(newAllMsgs, *triggerMsg)
	}

	freed := 0
	for _, m := range allMsgs[1:] {
		freed += len(m.Content)
	}
	out = fmt.Sprintf("History compacted (session %d → %d). Freed ~%d bytes.", prevSessionID, sess.id, freed)
	return out, newAllMsgs
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
