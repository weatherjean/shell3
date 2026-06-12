package chat

import (
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/weatherjean/shell3/internal/llm"
)

// Session holds the in-progress conversation history and the event stream.
// Exported so embedders and the TUI harness can subscribe to events and read
// the underlying store session id without going through internal helpers.
type Session struct {
	// msgMu guards the cross-goroutine append-vs-read on messages: the turn
	// goroutine appends (and reads sequentially, no self-race) while a second
	// reader — the Telegram dashboard polling History() — copies the slice
	// concurrently. Only that pair needs guarding; the turn loop's own reads
	// stay lock-free. Kept separate from inboxMu (different invariant, avoids a
	// lock-order coupling).
	msgMu    sync.RWMutex
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

	// inbox is the cross-goroutine message queue for a session: Interject pushes
	// items (steering text plus optional media parts) from any goroutine; the
	// turn loop drains on the turn goroutine at round boundaries. Guarded by
	// inboxMu — the only Session state that may be touched concurrently with a
	// running turn.
	inboxMu sync.Mutex
	inbox   []inboxItem
}

// inboxItem is one queued Interject: steering text plus optional media parts.
type inboxItem struct {
	text  string
	parts []llm.ContentPart
}

// Interject queues text for delivery to the model: mid-turn at the next round
// boundary, otherwise at the start of the next turn. Optional parts
// (images/audio) are delivered alongside the text via a synthetic user message
// — see drainInbox's callers. Safe to call from any goroutine at any time; it
// never fails and never blocks on a running turn.
func (s *Session) Interject(text string, parts ...llm.ContentPart) {
	s.inboxMu.Lock()
	defer s.inboxMu.Unlock()
	s.inbox = append(s.inbox, inboxItem{text: text, parts: slices.Clone(parts)})
}

// HasInbox reports whether any interjected items are queued. Safe to call from
// any goroutine.
func (s *Session) HasInbox() bool {
	s.inboxMu.Lock()
	defer s.inboxMu.Unlock()
	return len(s.inbox) > 0
}

// drainInbox removes all queued interjections, returning the steering texts
// (in arrival order, feeding interjectReminder) and the flattened media parts
// (same order, feeding attachmentsMessage). Called only from the turn
// goroutine.
func (s *Session) drainInbox() (texts []string, parts []llm.ContentPart) {
	s.inboxMu.Lock()
	defer s.inboxMu.Unlock()
	for _, it := range s.inbox {
		texts = append(texts, it.text)
		parts = append(parts, it.parts...)
	}
	s.inbox = nil
	return texts, parts
}

// interjectReminder formats queued interjections as one system-reminder block.
// Returns "" when items is empty or all items are blank after trimming. Each
// item is trimmed with strings.TrimSpace; whitespace-only items are skipped.
// Multi-line items have their continuation lines indented two spaces so the
// bullet list stays readable.
func interjectReminder(items []string) string {
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	wrote := false
	for _, it := range items {
		it = strings.TrimSpace(it)
		if it == "" {
			continue
		}
		if !wrote {
			b.WriteString("<system-reminder>\nuser sent additional input — incorporate it before continuing:\n")
			wrote = true
		}
		b.WriteString("- " + strings.ReplaceAll(it, "\n", "\n  ") + "\n")
	}
	if !wrote {
		return ""
	}
	b.WriteString("</system-reminder>")
	return b.String()
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
	// InitialMessages seeds the conversation when resuming a stored session.
	// Applied verbatim as the starting in-memory history before the first turn.
	InitialMessages []llm.Message
}

// NewSession constructs a Session that delivers events to opts.Sink. A nil Sink
// installs a no-op so emits are always safe. Other fields are optional.
func NewSession(opts SessionOpts) *Session {
	s := &Session{id: opts.StoreID, sink: opts.Sink}
	s.reminders.contextWindowFor = opts.ContextWindowFor
	if s.sink == nil {
		s.sink = func(Event) {}
	}
	if len(opts.InitialMessages) > 0 {
		s.messages = append(s.messages, opts.InitialMessages...)
	}
	return s
}

// ID returns the store session id (0 if no store is configured).
func (s *Session) ID() int64 {
	return s.id
}

func (s *Session) append(m llm.Message) {
	s.msgMu.Lock()
	defer s.msgMu.Unlock()
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
// in msgs. Returns msgs unchanged if reminder is empty. If no user message
// exists (e.g. a purely inbox-seeded wake turn whose empty carrier message was
// not appended to history), the reminder is appended as a fresh user message so
// the queued text still reaches the model. Operates on the allMsgs slice only —
// never on sess.messages. When the user message is multimodal the reminder is
// mirrored into its text part — the adapter sends ContentParts and ignores
// Content — on a cloned parts slice so the message stored in sess.messages
// stays reminder-free.
func injectReminder(msgs []llm.Message, reminder string) []llm.Message {
	if reminder == "" {
		return msgs
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == llm.RoleUser {
			msgs[i].Content = msgs[i].Content + "\n\n" + reminder
			if len(msgs[i].ContentParts) > 0 {
				parts := slices.Clone(msgs[i].ContentParts)
				appended := false
				for j := range parts {
					if parts[j].Type == llm.ContentPartTypeText {
						parts[j].Text += "\n\n" + reminder
						appended = true
						break
					}
				}
				if !appended {
					parts = append(parts, llm.ContentPart{Type: llm.ContentPartTypeText, Text: reminder})
				}
				msgs[i].ContentParts = parts
			}
			return msgs
		}
	}
	// No trailing user message (inbox-seeded wake turn): carry the queued text
	// to the model as a fresh user message rather than silently dropping it.
	return append(msgs, llm.Message{Role: llm.RoleUser, Content: reminder})
}
