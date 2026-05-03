package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/store"
	"github.com/weatherjean/shell3/internal/usertools"
)

func dispatchUserTool(ctx context.Context, tool usertools.Tool, rawArgs string, secrets map[string]string, workDir string) string {
	out, err := usertools.Run(ctx, tool, rawArgs, secrets, workDir)
	if err != nil {
		if out != "" {
			return out + "\nerror: " + err.Error()
		}
		return "error: " + err.Error()
	}
	return out
}

// truncateOutputMaxLines and truncateOutputMaxBytes cap how much of a tool
// result is shown inline in the TUI. The full result is always sent to the
// model; this only affects the user-visible display.
const (
	truncateOutputMaxLines = 3
	truncateOutputMaxBytes = 300
)

func truncateOutput(s string) string {
	lines := strings.Split(s, "\n")
	// Walk lines, stopping at whichever limit hits first.
	var kept []string
	used := 0
	for i, l := range lines {
		if i >= truncateOutputMaxLines {
			remaining := strings.Join(lines[i:], "\n")
			return strings.Join(kept, "\n") + fmt.Sprintf("\n… (+%d lines)\n", strings.Count(remaining, "\n")+1)
		}
		if used+len(l)+1 > truncateOutputMaxBytes {
			leftover := len(s) - used
			return strings.Join(kept, "\n") + fmt.Sprintf("\n… (+%d bytes)\n", leftover)
		}
		kept = append(kept, l)
		used += len(l) + 1 // +1 for newline
	}
	return s
}

// handleCompactHistory replaces the conversation history with a structured
// summary. Ends the current store session and starts a new one so the compact
// boundary is visible in history. Both sess.messages and allMsgs are rebuilt
// in place; the full compact args are saved to history before the session rolls.
func handleCompactHistory(rawArgs string, st *store.Store, sess *session, allMsgs []llm.Message, lg applog.Logger) (out string, newAllMsgs []llm.Message) {
	var args struct {
		Summary             string   `json:"summary"`
		ImportantFiles      []string `json:"important_files"`
		ImportantReferences []string `json:"important_references"`
		Skills              []string `json:"skills"`
		NextSteps           []string `json:"next_steps"`
	}
	if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
		return fmt.Sprintf("error: bad arguments: %v", err), allMsgs
	}
	if strings.TrimSpace(args.Summary) == "" {
		return "error: summary is required", allMsgs
	}

	prevSessionID := sess.id

	// Roll the store session so compact boundary is visible in history.
	if st != nil {
		// Flush current session messages before wiping — saveHistory bails early
		// after compact because prevLen > len(sess.messages), so we save here.
		for _, m := range sess.messages {
			switch m.Role {
			case llm.RoleUser, llm.RoleAssistant:
				_ = st.AppendHistory(prevSessionID, string(m.Role), m.Content)
				for _, tc := range m.ToolCalls {
					_ = st.AppendHistory(prevSessionID, "tool", toolCallSummary(tc))
				}
			}
		}
		// Save the compact call itself as the final entry in the outgoing session.
		_ = st.AppendHistory(prevSessionID, "tool", "compact_history: "+rawArgs)
		if err := st.EndSession(prevSessionID); err != nil {
			lg.Warn("end session failed during compact", "session_id", prevSessionID, "error", err)
		}
		newID, err := st.StartSession()
		if err == nil {
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
