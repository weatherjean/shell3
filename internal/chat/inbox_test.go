package chat

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
	"github.com/weatherjean/shell3/internal/persona"
)

// TestInterject_IdleQueuesForNextTurn: an interject pushed while no turn is
// running is injected at the start of the next turn — visible to the model in
// the user message and surfaced as a SystemReminder event.
func TestInterject_IdleQueuesForNextTurn(t *testing.T) {
	fake := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "ok"}}})
	sess, c := newCollectorSession(SessionOpts{})
	sess.Interject("actually use repo B")

	cfg := TurnConfig{LLM: fake, Personality: persona.Persona{SystemPrompt: "t"}, Log: LogOrNoop(nil)}
	RunTurn(context.Background(), cfg, sess, llm.Message{Role: llm.RoleUser, Content: "hi"}, nil)

	events := c.all()
	var sawReminder bool
	for _, ev := range events {
		if ev.Kind == EventSystemReminder && strings.Contains(ev.Text, "actually use repo B") {
			sawReminder = true
		}
	}
	if !sawReminder {
		t.Fatalf("queued interject should surface as a system-reminder event; events=%+v", events)
	}
	// The model-visible injection lands on the turn's user message copy, not
	// on the session's persisted history.
	for _, m := range sess.messages {
		if strings.Contains(m.Content, "user sent additional input") {
			t.Fatalf("interject reminder leaked into persisted history: %q", m.Content)
		}
	}
}

// TestInterject_MidTurnInjectsNextRound: an interject pushed while a tool round
// is executing is delivered before the next LLM round.
func TestInterject_MidTurnInjectsNextRound(t *testing.T) {
	fake := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{
			{ToolCall: &llm.ToolCall{ID: "a", Name: "echo", RawArgs: `{}`}},
		}},
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "adjusted"}}},
	)
	sess, c := newCollectorSession(SessionOpts{})
	cfg := TurnConfig{
		LLM:         fake,
		Personality: persona.Persona{SystemPrompt: "t"},
		Handlers: map[string]ToolHandler{"echo": funcHandler{name: "echo",
			fn: func(context.Context, string, json.RawMessage, ToolConfig) (string, error) {
				sess.Interject("stop, wrong file")
				return "echoed", nil
			}}},
		Log: LogOrNoop(nil),
	}
	RunTurn(context.Background(), cfg, sess, llm.Message{Role: llm.RoleUser, Content: "go"}, nil)

	// Order: tool_result for echo, THEN the interject reminder, THEN tokens.
	events := c.all()
	toolIdx, remIdx := -1, -1
	for i, ev := range events {
		if ev.Kind == EventToolResult && toolIdx == -1 {
			toolIdx = i
		}
		if ev.Kind == EventSystemReminder && strings.Contains(ev.Text, "stop, wrong file") {
			remIdx = i
		}
	}
	if toolIdx == -1 || remIdx == -1 || remIdx < toolIdx {
		t.Fatalf("interject must inject after the tool round (tool=%d, reminder=%d)", toolIdx, remIdx)
	}
}

// TestInterject_MultipleInterjectionsDrainIntoOneReminder: two Interject calls
// before a turn produce a single EventSystemReminder containing both bullets
// (not two separate reminder events).
func TestInterject_MultipleInterjectionsDrainIntoOneReminder(t *testing.T) {
	fake := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "ok"}}})
	sess, c := newCollectorSession(SessionOpts{})
	sess.Interject("first note")
	sess.Interject("second note")

	cfg := TurnConfig{LLM: fake, Personality: persona.Persona{SystemPrompt: "t"}, Log: LogOrNoop(nil)}
	RunTurn(context.Background(), cfg, sess, llm.Message{Role: llm.RoleUser, Content: "hi"}, nil)

	events := c.all()
	var reminderEvents []Event
	for _, ev := range events {
		if ev.Kind == EventSystemReminder {
			reminderEvents = append(reminderEvents, ev)
		}
	}
	if len(reminderEvents) != 1 {
		t.Fatalf("expected exactly 1 reminder event, got %d: %+v", len(reminderEvents), reminderEvents)
	}
	rem := reminderEvents[0].Text
	if !strings.Contains(rem, "first note") || !strings.Contains(rem, "second note") {
		t.Fatalf("single reminder should contain both bullets; got: %q", rem)
	}
}

// TestInterject_CrossGoroutine: Interject called from a separate goroutine
// while a tool handler is executing is safely serialized by the mutex and
// appears as a reminder in that same turn. The -race detector exercises the
// inboxMu critical section.
func TestInterject_CrossGoroutine(t *testing.T) {
	fake := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{
			{ToolCall: &llm.ToolCall{ID: "b", Name: "work", RawArgs: `{}`}},
		}},
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "done"}}},
	)
	sess, c := newCollectorSession(SessionOpts{})
	cfg := TurnConfig{
		LLM:         fake,
		Personality: persona.Persona{SystemPrompt: "t"},
		Handlers: map[string]ToolHandler{"work": funcHandler{name: "work",
			fn: func(context.Context, string, json.RawMessage, ToolConfig) (string, error) {
				// Push from a separate goroutine; wait for it to finish before
				// the handler returns so the interject always lands before the
				// next LLM round.
				done := make(chan struct{})
				go func() {
					sess.Interject("from goroutine")
					close(done)
				}()
				<-done
				return "worked", nil
			}}},
		Log: LogOrNoop(nil),
	}
	RunTurn(context.Background(), cfg, sess, llm.Message{Role: llm.RoleUser, Content: "go"}, nil)

	events := c.all()
	var sawReminder bool
	for _, ev := range events {
		if ev.Kind == EventSystemReminder && strings.Contains(ev.Text, "from goroutine") {
			sawReminder = true
		}
	}
	if !sawReminder {
		t.Fatalf("cross-goroutine Interject must surface as a reminder; events=%+v", events)
	}
}

// TestInterjectNotice_DeliveredWithNoticeHeader: a host notification surfaces at
// turn start under its own header — never labeled as user input.
func TestInterjectNotice_DeliveredWithNoticeHeader(t *testing.T) {
	fake := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "ok"}}})
	sess, c := newCollectorSession(SessionOpts{})
	sess.InterjectNotice("subagent x finished (done). Result: built the thing")

	cfg := TurnConfig{LLM: fake, Personality: persona.Persona{SystemPrompt: "t"}, Log: LogOrNoop(nil)}
	RunTurn(context.Background(), cfg, sess, llm.Message{Role: llm.RoleUser, Content: "hi"}, nil)

	var rem string
	for _, ev := range c.all() {
		if ev.Kind == EventSystemReminder && strings.Contains(ev.Text, "subagent x finished") {
			rem = ev.Text
		}
	}
	if rem == "" {
		t.Fatalf("notice should surface as a system-reminder at turn start; events=%+v", c.all())
	}
	if !strings.Contains(rem, "a background task you started reported back") {
		t.Fatalf("notice must use the host-notification header: %q", rem)
	}
	if strings.Contains(rem, "user sent additional input") {
		t.Fatalf("notice must NOT be labeled as user input: %q", rem)
	}
}

// TestInterjectNotice_NotDeliveredMidTurn: a notification that arrives during a
// tool round must NOT be injected at the after-tool-round boundary (unlike user
// steering) — it stays queued for a turn boundary so it can't interrupt the
// in-flight turn.
func TestInterjectNotice_NotDeliveredMidTurn(t *testing.T) {
	fake := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{
			{ToolCall: &llm.ToolCall{ID: "a", Name: "echo", RawArgs: `{}`}},
		}},
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "done"}}},
	)
	sess, c := newCollectorSession(SessionOpts{})
	cfg := TurnConfig{
		LLM:         fake,
		Personality: persona.Persona{SystemPrompt: "t"},
		Handlers: map[string]ToolHandler{"echo": funcHandler{name: "echo",
			fn: func(context.Context, string, json.RawMessage, ToolConfig) (string, error) {
				sess.InterjectNotice("subagent bg1 finished (done).")
				return "echoed", nil
			}}},
		Log: LogOrNoop(nil),
	}
	RunTurn(context.Background(), cfg, sess, llm.Message{Role: llm.RoleUser, Content: "go"}, nil)

	for _, ev := range c.all() {
		if ev.Kind == EventSystemReminder && strings.Contains(ev.Text, "subagent bg1 finished") {
			t.Fatalf("a notice must not be injected mid-turn; got reminder: %q", ev.Text)
		}
	}
	// It stays queued — a later turn/wake boundary delivers it.
	if !sess.HasInbox() {
		t.Fatal("notice should remain queued after the turn's mid-round drain")
	}
}

// TestInterject_WhitespaceOnly_NoSystemReminder: an Interject containing only
// whitespace must not produce a SystemReminder event — the reminder block
// should be suppressed entirely (no header-only XML block).
func TestInterject_WhitespaceOnly_NoSystemReminder(t *testing.T) {
	fake := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "ok"}}})
	sess, c := newCollectorSession(SessionOpts{})
	sess.Interject("   ") // whitespace only

	cfg := TurnConfig{LLM: fake, Personality: persona.Persona{SystemPrompt: "t"}, Log: LogOrNoop(nil)}
	RunTurn(context.Background(), cfg, sess, llm.Message{Role: llm.RoleUser, Content: "hi"}, nil)

	events := c.all()
	for _, ev := range events {
		if ev.Kind == EventSystemReminder {
			t.Fatalf("whitespace-only Interject produced a SystemReminder event; text=%q", ev.Text)
		}
	}
}

// TestSession_DropInbox pins the /clear drain: queued interjections and
// notices are discarded wholesale so nothing from the cleared conversation
// leaks into the fresh session.
func TestSession_DropInbox(t *testing.T) {
	sess, _ := newCollectorSession(SessionOpts{})
	sess.Interject("steer me")
	sess.InterjectNotice("bg1 finished")
	if !sess.HasInbox() {
		t.Fatal("expected a non-empty inbox after queueing")
	}
	sess.DropInbox()
	if sess.HasInbox() {
		t.Fatal("DropInbox should discard every queued item")
	}
}
