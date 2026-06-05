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
	// nextToolCallID drives sequential numeric ids ("1", "2", ...) that replace
	// provider-emitted ids. See turn.go (allocToolCallID call site) for why.
	nextToolCallID   int
	reminders        reminderTracker
	lastPromptTokens int   // accurate token count from most recent streamOnce response
	id               int64 // store session id; 0 if no store configured

	// sink receives every event synchronously, inline on the goroutine that
	// runs the turn. There is no channel and no teardown: once Run returns,
	// every event has already been delivered. Always non-nil (NewSession
	// installs a no-op when SessionOpts.Sink is unset).
	sink func(Event)
}

// SessionOpts configures a new Session. All fields are optional.
//
// StoreID is the running session id returned by store.Store.StartSession;
// embedders that don't use a store can leave it zero.
// ContextWindowFor resolves a model id to its context window in tokens;
// the reminder tracker uses it to emit context-usage reminders.
// Sink receives every event synchronously, inline on the turn goroutine. When
// nil, events are discarded (a no-op sink is installed).
type SessionOpts struct {
	StoreID          int64
	ContextWindowFor func(string) int
	Sink             func(Event)
}

// NewSession constructs a Session that delivers events to opts.Sink. A nil Sink
// installs a no-op so emits are always safe. Other fields are optional.
func NewSession(opts SessionOpts) *Session {
	s := &Session{id: opts.StoreID, sink: opts.Sink}
	s.reminders.contextWindowFor = opts.ContextWindowFor
	if s.sink == nil {
		s.sink = func(Event) {}
	}
	return s
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
