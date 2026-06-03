package chat

import (
	"fmt"
	"strings"

	"github.com/weatherjean/shell3/internal/llm"
)

// Session holds the in-progress conversation history and the event stream.
// Exported so embedders and the TUI harness can subscribe to events and read
// the underlying store session id without going through internal helpers.
type Session struct {
	messages []llm.Message
	// nextToolCallID drives sequential numeric ids ("1", "2", ...) that
	// replace whatever the provider emits. Models reliably echo a bare
	// integer; provider-native ids like "web_fetch:0" get truncated by
	// models and break tool_call_id-based addressing (e.g. prune_tool_result).
	nextToolCallID   int
	reminders        reminderTracker
	lastPromptTokens int   // accurate token count from most recent streamOnce response
	id               int64 // store session id; 0 if no store configured
	events           chan Event
}

// SessionOpts configures a new Session. All fields are optional.
//
// BufSize controls event-channel back-pressure: too small blocks the turn
// loop, too large hides slow consumers. Defaults to 256 when zero.
// StoreID is the running session id returned by store.Store.StartSession;
// embedders that don't use a store can leave it zero.
// ContextWindowFor resolves a model id to its context window in tokens;
// the reminder tracker uses it to emit context-usage reminders.
type SessionOpts struct {
	BufSize          int
	StoreID          int64
	ContextWindowFor func(string) int
}

// NewSession constructs a Session with a buffered event channel.
// opts.BufSize defaults to 256 when zero; other fields are optional.
func NewSession(opts SessionOpts) *Session {
	if opts.BufSize == 0 {
		opts.BufSize = 256
	}
	s := &Session{
		events: make(chan Event, opts.BufSize),
		id:     opts.StoreID,
	}
	s.reminders.contextWindowFor = opts.ContextWindowFor
	return s
}

// Events returns the read-only event channel for this session. Consumers
// (TUI, JSONL sink, embedders) range over this channel until the session
// closes. The channel is closed exactly once during teardown.
func (s *Session) Events() <-chan Event {
	return s.events
}

// ID returns the store session id (0 if no store is configured).
func (s *Session) ID() int64 {
	return s.id
}

func (s *Session) append(m llm.Message) {
	s.messages = append(s.messages, m)
}

// allocToolCallID returns the next sequential numeric tool-call id as
// a decimal string.
func (s *Session) allocToolCallID() string {
	s.nextToolCallID++
	return fmt.Sprintf("%d", s.nextToolCallID)
}

// reminderTracker decides when to emit <system-reminder> injections and
// remembers what was last sent so we don't repeat unchanged state.
type reminderTracker struct {
	lastContextPct   int    // last 10%-bucket emitted (0 = never emitted)
	lastModel        string // model name present in last emitted reminder
	lastTokens       int    // prompt tokens at last emission (persists across turns)
	contextWindowFor func(string) int
}

// check returns a formatted <system-reminder> block if any condition warrants
// one, and updates tracker state. Returns "" when nothing changed enough to
// warrant a reminder.
//
// statusLine is cfg.StatusLine (e.g. "openai/claude-sonnet-4-6").
// promptTokens is the most recent prompt token count (0 = unknown).
func (r *reminderTracker) check(statusLine string, promptTokens int) string {
	_, model := SplitStatus(statusLine)
	contextWindow := 0
	if r.contextWindowFor != nil {
		contextWindow = r.contextWindowFor(model)
	}

	var lines []string

	// Model change reminder.
	if model != "" && model != r.lastModel && r.lastModel != "" {
		lines = append(lines, fmt.Sprintf("model changed: %s → %s", r.lastModel, model))
	}

	// Context usage reminder — every 10% of context window or every 30k tokens,
	// whichever triggers first. Only fires when we have real usage data.
	if promptTokens > 0 && contextWindow > 0 {
		pct := promptTokens * 100 / contextWindow
		bucket := (pct / 10) * 10 // round down to nearest 10
		tokenDelta := promptTokens - r.lastTokens
		if bucket > r.lastContextPct || (tokenDelta >= 30000 && r.lastContextPct > 0) {
			lines = append(lines, fmt.Sprintf(
				"context: %d / %d tokens (%d%%)",
				promptTokens, contextWindow, pct,
			))
			r.lastContextPct = bucket
			r.lastTokens = promptTokens
		}
	}

	// Update model regardless of whether we emitted a reminder.
	if model != "" {
		r.lastModel = model
	}

	if len(lines) == 0 {
		return ""
	}
	return "<system-reminder>\n" + strings.Join(lines, "\n") + "\n</system-reminder>"
}

// injectReminder appends a <system-reminder> block to the last user message
// in msgs. Returns msgs unchanged if reminder is empty or no user message exists.
// Operates on the allMsgs slice only — never on sess.messages.
func injectReminder(msgs []llm.Message, reminder string) []llm.Message {
	if reminder == "" {
		return msgs
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == llm.RoleUser {
			msgs[i].Content = msgs[i].Content + "\n\n" + reminder
			return msgs
		}
	}
	return msgs
}
