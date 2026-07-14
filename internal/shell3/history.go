package shell3

import (
	"strings"
	"time"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/strutil"
)

// HistoryEntry is one stored conversation message, projected for introspection
// (the dashboard's conversation view / replay). Content is already stripped of
// the internal "[tool_call_id=…]\n" storage prefix that tool results carry.
// Role is the plain string "user"/"assistant"/"tool"/"system".
type HistoryEntry struct {
	Role       string
	Content    string
	ToolName   string
	ToolCallID string
	// ToolCalls holds an assistant message's tool invocations (name + raw JSON
	// args). Empty for non-assistant messages or assistant messages with no calls.
	ToolCalls []ToolCallInfo
	// Reasoning is the assistant's chain-of-thought text, when the provider
	// emits it (reasoning_content). Empty otherwise.
	Reasoning string
}

// ToolCallInfo is one tool invocation made by an assistant message.
type ToolCallInfo struct {
	ID   string
	Name string
	Args string // raw JSON arguments
}

// History returns the current conversation history as public HistoryEntry
// values. Tool-role messages have their internal "[tool_call_id=…]\n" prefix
// stripped from Content so consumers see the raw tool output. Safe to call
// concurrently with a running turn: it reads a single locked snapshot via
// chat.Session.HistorySnapshot (a front-end may poll it mid-turn), so a
// concurrent compaction can't split the message slice from its reminder anchors.
func (s *Session) History() []HistoryEntry {
	msgs, rems := s.sess.HistorySnapshot()
	out := make([]HistoryEntry, 0, len(msgs)+len(rems))
	// Interleave recorded system-reminders ahead of the message index they were
	// injected before. rems is append-ordered, so Seq is non-decreasing.
	ri := 0
	flush := func(upto int) {
		for ri < len(rems) && rems[ri].Seq <= upto {
			out = append(out, HistoryEntry{Role: "system", Content: rems[ri].Text})
			ri++
		}
	}
	for i, m := range msgs {
		flush(i)
		out = append(out, messageToEntry(m))
	}
	flush(len(msgs)) // trailing reminders (mid-turn, before the reply lands)
	return out
}

// messageToEntry projects one internal message to the public HistoryEntry,
// stripping the tool-result id prefix and carrying tool calls + reasoning.
// Shared by History (live) and SessionMessages (stored replay).
func messageToEntry(m llm.Message) HistoryEntry {
	content := m.Content
	if m.Role == llm.RoleTool {
		content = stripToolIDPrefix(content)
	}
	calls := make([]ToolCallInfo, 0, len(m.ToolCalls))
	for _, tc := range m.ToolCalls {
		calls = append(calls, ToolCallInfo{ID: tc.ID, Name: tc.Name, Args: tc.RawArgs})
	}
	return HistoryEntry{
		Role:       string(m.Role),
		Content:    content,
		ToolName:   m.Name,
		ToolCallID: m.ToolCallID,
		ToolCalls:  calls,
		Reasoning:  m.ReasoningContent,
	}
}

// stripToolIDPrefix removes the "[tool_call_id=…]\n" prefix the turn loop
// prepends to each stored tool result's content, leaving just the raw output,
// so the public projection in History hides the internal storage detail.
func stripToolIDPrefix(content string) string {
	if strings.HasPrefix(content, "[tool_call_id=") {
		if nl := strings.IndexByte(content, '\n'); nl >= 0 {
			return content[nl+1:]
		}
	}
	return content
}

// SessionMeta is one stored session's metadata, for the dashboard's session
// list (Runs tab). LastAt is the recency sort key.
type SessionMeta struct {
	ID        string    `json:"id"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at,omitzero"` // zero while the session is still open
	LastAt    time.Time `json:"last_at"`           // newest message; falls back to start
	NumMsgs   int       `json:"num_msgs"`
	Preview   string    `json:"preview"`
}

// PastSessions returns up to limit stored sessions, newest first, for the
// dashboard's session list. nil when the runtime has no store.
func (rt *Runtime) PastSessions(limit int) ([]SessionMeta, error) {
	if rt.store == nil {
		return nil, nil
	}
	rows, err := rt.store.ListSessions(limit)
	if err != nil {
		return nil, err
	}
	out := make([]SessionMeta, 0, len(rows))
	for _, m := range rows {
		e := SessionMeta{ID: m.ID, StartedAt: m.StartedAt.UTC()}
		if !m.EndedAt.IsZero() {
			e.EndedAt = m.EndedAt.UTC()
		}
		last := m.LastAt
		if last.IsZero() {
			last = m.StartedAt // no messages yet — sort by when it started
		}
		e.LastAt = last.UTC()
		// Derive the message count + a preview (newest assistant/user text) from
		// the run's jsonl. Best-effort: a read error leaves NumMsgs 0 / Preview "".
		if msgs, err := rt.store.LoadMessages(m.ID); err == nil {
			e.NumMsgs = len(msgs)
			e.Preview = previewOf(msgs)
		}
		out = append(out, e)
	}
	return out, nil
}

// SessionMessages returns a stored session's messages as HistoryEntry values,
// for the dashboard's conversation replay. nil when the runtime has no store.
func (rt *Runtime) SessionMessages(id string) ([]HistoryEntry, error) {
	if rt.store == nil {
		return nil, nil
	}
	msgs, err := rt.store.LoadMessages(id)
	if err != nil {
		return nil, err
	}
	out := make([]HistoryEntry, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, messageToEntry(m))
	}
	return out, nil
}

// previewOf returns the newest user/assistant text in msgs, truncated to a
// run-list preview budget. "" when there is no such message.
func previewOf(msgs []llm.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if (m.Role == llm.RoleUser || m.Role == llm.RoleAssistant) && strings.TrimSpace(m.Content) != "" {
			return strutil.Truncate(strings.TrimSpace(m.Content), 200)
		}
	}
	return ""
}
