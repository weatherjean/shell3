package chat

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
)

func TestInterject_IdleQueuesForNextTurn(t *testing.T) {
	fake := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "ok"}}})
	sess, c := newCollectorSession(SessionOpts{})
	sess.Interject("actually use repo B")

	cfg := TurnConfig{LLM: fake, Profile: AgentProfile{SystemPrompt: "t"}, ToolConfig: ToolConfig{Log: LogOrNoop(nil)}}
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
	for _, m := range sess.messages {
		if strings.Contains(m.Content, "user sent additional input") {
			t.Fatalf("interject reminder leaked into persisted history: %q", m.Content)
		}
	}
}

func TestInterject_MidTurnInjectsNextRound(t *testing.T) {
	fake := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{
			{ToolCall: &llm.ToolCall{ID: "a", Name: "echo", RawArgs: `{}`}},
		}},
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "adjusted"}}},
	)
	sess, c := newCollectorSession(SessionOpts{})
	cfg := TurnConfig{
		LLM:     fake,
		Profile: AgentProfile{SystemPrompt: "t"},
		Handlers: map[string]ToolHandler{"echo": funcHandler{name: "echo",
			fn: func(context.Context, string, json.RawMessage, ToolConfig) (string, error) {
				sess.Interject("stop, wrong file")
				return "echoed", nil
			}}},
		ToolConfig: ToolConfig{Log: LogOrNoop(nil)},
	}
	RunTurn(context.Background(), cfg, sess, llm.Message{Role: llm.RoleUser, Content: "go"}, nil)

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

func TestInterject_MultipleInterjectionsDrainIntoOneReminder(t *testing.T) {
	fake := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "ok"}}})
	sess, c := newCollectorSession(SessionOpts{})
	sess.Interject("first note")
	sess.Interject("second note")

	cfg := TurnConfig{LLM: fake, Profile: AgentProfile{SystemPrompt: "t"}, ToolConfig: ToolConfig{Log: LogOrNoop(nil)}}
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

func TestInterject_CrossGoroutine(t *testing.T) {
	fake := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{
			{ToolCall: &llm.ToolCall{ID: "b", Name: "work", RawArgs: `{}`}},
		}},
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "done"}}},
	)
	sess, c := newCollectorSession(SessionOpts{})
	cfg := TurnConfig{
		LLM:     fake,
		Profile: AgentProfile{SystemPrompt: "t"},
		Handlers: map[string]ToolHandler{"work": funcHandler{name: "work",
			fn: func(context.Context, string, json.RawMessage, ToolConfig) (string, error) {
				done := make(chan struct{})
				go func() {
					sess.Interject("from goroutine")
					close(done)
				}()
				<-done
				return "worked", nil
			}}},
		ToolConfig: ToolConfig{Log: LogOrNoop(nil)},
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

func TestInterjectHostNoticeDeliveredWithNoticeHeader(t *testing.T) {
	fake := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "ok"}}})
	sess, c := newCollectorSession(SessionOpts{})
	sess.InterjectHostNotice("[superstop] background commands stopped")

	cfg := TurnConfig{LLM: fake, Profile: AgentProfile{SystemPrompt: "t"}, ToolConfig: ToolConfig{Log: LogOrNoop(nil)}}
	RunTurn(context.Background(), cfg, sess, llm.Message{Role: llm.RoleUser, Content: "hi"}, nil)

	var rem string
	for _, ev := range c.all() {
		if ev.Kind == EventSystemReminder && strings.Contains(ev.Text, "superstop") {
			rem = ev.Text
		}
	}
	if rem == "" {
		t.Fatalf("notice should surface as a system-reminder at turn start; events=%+v", c.all())
	}
	if !strings.Contains(rem, "shell3 host notice") {
		t.Fatalf("notice must use the host-notification header: %q", rem)
	}
	if strings.Contains(rem, "user sent additional input") {
		t.Fatalf("notice must NOT be labeled as user input: %q", rem)
	}
}

func TestInterjectHostNoticeNotDeliveredMidTurn(t *testing.T) {
	fake := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{
			{ToolCall: &llm.ToolCall{ID: "a", Name: "echo", RawArgs: `{}`}},
		}},
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "done"}}},
	)
	sess, c := newCollectorSession(SessionOpts{})
	cfg := TurnConfig{
		LLM:     fake,
		Profile: AgentProfile{SystemPrompt: "t"},
		Handlers: map[string]ToolHandler{"echo": funcHandler{name: "echo",
			fn: func(context.Context, string, json.RawMessage, ToolConfig) (string, error) {
				sess.InterjectHostNotice("[superstop] background commands stopped")
				return "echoed", nil
			}}},
		ToolConfig: ToolConfig{Log: LogOrNoop(nil)},
	}
	RunTurn(context.Background(), cfg, sess, llm.Message{Role: llm.RoleUser, Content: "go"}, nil)

	for _, ev := range c.all() {
		if ev.Kind == EventSystemReminder && strings.Contains(ev.Text, "superstop") {
			t.Fatalf("a notice must not be injected mid-turn; got reminder: %q", ev.Text)
		}
	}
	if !sess.HasInbox() {
		t.Fatal("notice should remain queued after the turn's mid-round drain")
	}
}

func TestInterject_WhitespaceOnly_NoSystemReminder(t *testing.T) {
	fake := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "ok"}}})
	sess, c := newCollectorSession(SessionOpts{})
	sess.Interject("   ")

	cfg := TurnConfig{LLM: fake, Profile: AgentProfile{SystemPrompt: "t"}, ToolConfig: ToolConfig{Log: LogOrNoop(nil)}}
	RunTurn(context.Background(), cfg, sess, llm.Message{Role: llm.RoleUser, Content: "hi"}, nil)

	events := c.all()
	for _, ev := range events {
		if ev.Kind == EventSystemReminder {
			t.Fatalf("whitespace-only Interject produced a SystemReminder event; text=%q", ev.Text)
		}
	}
}
