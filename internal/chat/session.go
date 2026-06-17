package chat

import (
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/runs"
)

// Session holds the in-progress conversation history and the event stream.
// Exported so embedders and the TUI harness can subscribe to events and read
// the underlying store session id without going through internal helpers.
type Session struct {
	// msgMu guards the cross-goroutine append-vs-read on messages: the turn
	// goroutine appends while a second reader — the Telegram dashboard polling
	// History() — copies the slice concurrently. The turn loop's own reads stay
	// lock-free. Kept separate from inboxMu to avoid a lock-order coupling.
	msgMu    sync.RWMutex
	messages []llm.Message
	// standingReminders holds host-level "standing" reminders (Environment,
	// Delegation context) set by SetStandingReminders. They are injected into
	// every turn's allMsgs and exposed via Reminders() for the dashboard, but
	// are NOT persisted to the sidecar — they regenerate on session resume.
	// Guarded by msgMu.
	standingReminders []string
	// nextToolCallID drives sequential numeric ids ("1", "2", ...) that replace
	// provider-emitted ids. See turn.go (allocToolCallID call site) for why.
	nextToolCallID int
	reminders      reminderTracker
	// reminderLog records each emitted <system-reminder> with the message index
	// it precedes (the assistant reply it was injected ahead of), so the dashboard
	// can interleave reminders into History() as system-role entries. Live-only
	// (in-memory); not persisted. Guarded by msgMu.
	reminderLog      []ReminderRecord
	lastPromptTokens int         // accurate token count from most recent streamOnce response
	id               string      // runs session id; "" if no store configured
	store            *runs.Store // optional; nil → no sidecar persistence
	// persistedLen is the count of sess.messages already written to the current
	// sess.id's messages.jsonl. Updated by saveHistory after each flush and by
	// compactInto after the session roll. Touched only on the turn goroutine
	// (same as sess.id) so no extra lock is required.
	persistedLen int

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
// StoreID is the runs session id returned by runs.Store.NewSession;
// embedders that don't use a store can leave it empty.
// ContextWindowFor resolves a model id to its context window in tokens;
// the reminder tracker uses it to emit context-usage reminders.
// Sink receives every event synchronously, inline on the turn goroutine. When
// nil, events are discarded (a no-op sink is installed).
type SessionOpts struct {
	StoreID          string
	ContextWindowFor func(string) int
	Sink             func(Event)
	// InitialMessages seeds the conversation when resuming a stored session.
	// Applied verbatim as the starting in-memory history before the first turn.
	InitialMessages []llm.Message
	// Store and ID wire sidecar persistence for reminders. When Store is non-nil
	// and ID is non-empty, recordReminder appends to runs/<ID>/reminders.jsonl
	// and RestoreReminders reloads it on resume.
	Store *runs.Store
	ID    string
}

// NewSession constructs a Session that delivers events to opts.Sink. A nil Sink
// installs a no-op so emits are always safe. Other fields are optional.
func NewSession(opts SessionOpts) *Session {
	id := opts.StoreID
	if id == "" {
		id = opts.ID
	}
	s := &Session{id: id, store: opts.Store, sink: opts.Sink}
	s.reminders.contextWindowFor = opts.ContextWindowFor
	if s.sink == nil {
		s.sink = func(Event) {}
	}
	if len(opts.InitialMessages) > 0 {
		s.messages = append(s.messages, opts.InitialMessages...)
	}
	return s
}

// ID returns the runs session id ("" if no store is configured).
func (s *Session) ID() string {
	return s.id
}

// SetID swaps the runs session id. Used by Session.Clear to rotate onto a fresh
// session so subsequent turns persist under the new conversation. Guarded by
// msgMu because the dashboard's History() reader pairs id with the message slice.
func (s *Session) SetID(id string) {
	s.msgMu.Lock()
	defer s.msgMu.Unlock()
	s.id = id
}

// SetStandingReminders replaces the host "standing" reminders (Environment,
// Delegation) — regenerated at every prompt-assembly, so they are recorded for
// the dashboard but NOT persisted (resume re-assembles them fresh).
func (s *Session) SetStandingReminders(texts []string) {
	s.msgMu.Lock()
	s.standingReminders = append(s.standingReminders[:0], texts...)
	s.msgMu.Unlock()
}

func (s *Session) append(m llm.Message) {
	s.msgMu.Lock()
	defer s.msgMu.Unlock()
	s.messages = append(s.messages, m)
}

// ReminderRecord is one emitted system-reminder, anchored to the message index
// it precedes (the assistant reply it was injected ahead of). The dashboard
// uses Seq to interleave it into the rendered history.
type ReminderRecord struct {
	Seq  int
	Text string
}

// recordReminder logs a system-reminder for dashboard display, anchored before
// the next message to be appended. Called from emitSystemReminder.
func (s *Session) recordReminder(text string) {
	s.msgMu.Lock()
	defer s.msgMu.Unlock()
	seq := len(s.messages)
	s.reminderLog = append(s.reminderLog, ReminderRecord{Seq: seq, Text: text})
	if s.store != nil && s.id != "" {
		_ = s.store.AppendReminder(s.id, seq, text) // best-effort; never blocks the turn
	}
}

// RestoreReminders reloads reminderLog from the persisted sidecar (resume path).
func (s *Session) RestoreReminders() error {
	if s.store == nil || s.id == "" {
		return nil
	}
	lines, err := s.store.LoadReminders(s.id)
	if err != nil {
		return err
	}
	s.msgMu.Lock()
	defer s.msgMu.Unlock()
	s.reminderLog = s.reminderLog[:0]
	for _, l := range lines {
		s.reminderLog = append(s.reminderLog, ReminderRecord{Seq: l.Seq, Text: l.Text})
	}
	return nil
}

// Reminders returns a snapshot of all system-reminders, safe to retain.
// Standing reminders (anchored at Seq 0) are prepended ahead of the logged
// ones so History() interleaves them at the top of the conversation.
// Safe to call concurrently with a running turn (mirrors Messages()).
func (s *Session) Reminders() []ReminderRecord {
	s.msgMu.RLock()
	defer s.msgMu.RUnlock()
	out := make([]ReminderRecord, 0, len(s.standingReminders)+len(s.reminderLog))
	for _, t := range s.standingReminders {
		out = append(out, ReminderRecord{Seq: 0, Text: t})
	}
	out = append(out, slices.Clone(s.reminderLog)...)
	return out
}

// StandingReminders returns a copy of the host standing reminders (Environment,
// Delegation) for display in the prompt-inspection views (the TUI /prompt
// command and the dashboard Status → Prompt). Safe to call concurrently.
func (s *Session) StandingReminders() []string {
	s.msgMu.RLock()
	defer s.msgMu.RUnlock()
	return slices.Clone(s.standingReminders)
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
