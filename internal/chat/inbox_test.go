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
	sess, _ := newCollectorSession(SessionOpts{})
	sess.Interject("actually use repo B")

	cfg := TurnConfig{LLM: fake, Profile: AgentProfile{SystemPrompt: "t"}, ToolConfig: ToolConfig{Log: LogOrNoop(nil)}}
	RunTurn(context.Background(), cfg, sess, llm.Message{Role: llm.RoleUser, Content: "hi"}, nil)

	if reminders := recordedReminderText(sess); !strings.Contains(reminders, "actually use repo B") {
		t.Fatalf("queued interject was not recorded: %q", reminders)
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
	sess, _ := newCollectorSession(SessionOpts{})
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

	if calls := fake.CallsSnapshot(); len(calls) != 2 || !providerCallsContain(calls[1:], "stop, wrong file") {
		t.Fatalf("interject was not injected into the next provider round: %+v", calls)
	}
}

func TestInterject_MultipleInterjectionsDrainIntoOneReminder(t *testing.T) {
	fake := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "ok"}}})
	sess, _ := newCollectorSession(SessionOpts{})
	sess.Interject("first note")
	sess.Interject("second note")

	cfg := TurnConfig{LLM: fake, Profile: AgentProfile{SystemPrompt: "t"}, ToolConfig: ToolConfig{Log: LogOrNoop(nil)}}
	RunTurn(context.Background(), cfg, sess, llm.Message{Role: llm.RoleUser, Content: "hi"}, nil)

	rems := reminderSnapshot(sess)
	if len(rems) != 1 {
		t.Fatalf("expected exactly 1 recorded reminder, got %d: %+v", len(rems), rems)
	}
	rem := rems[0].Text
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
	sess, _ := newCollectorSession(SessionOpts{})
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

	if !providerCallsContain(fake.CallsSnapshot(), "from goroutine") {
		t.Fatal("cross-goroutine Interject did not reach a provider round")
	}
}

func TestInterjectHostNoticeDeliveredWithNoticeHeader(t *testing.T) {
	fake := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "ok"}}})
	sess, _ := newCollectorSession(SessionOpts{})
	sess.InterjectHostNotice("[superstop] background commands stopped")

	cfg := TurnConfig{LLM: fake, Profile: AgentProfile{SystemPrompt: "t"}, ToolConfig: ToolConfig{Log: LogOrNoop(nil)}}
	RunTurn(context.Background(), cfg, sess, llm.Message{Role: llm.RoleUser, Content: "hi"}, nil)

	rem := recordedReminderText(sess)
	if rem == "" {
		t.Fatal("notice should be recorded as a system reminder at turn start")
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
	sess, _ := newCollectorSession(SessionOpts{})
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

	if rem := recordedReminderText(sess); strings.Contains(rem, "superstop") {
		t.Fatalf("a notice must not be injected mid-turn; got reminder: %q", rem)
	}
	if !sess.HasInbox() {
		t.Fatal("notice should remain queued after the turn's mid-round drain")
	}
}

func TestInterject_WhitespaceOnly_NoSystemReminder(t *testing.T) {
	fake := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "ok"}}})
	sess, _ := newCollectorSession(SessionOpts{})
	sess.Interject("   ")

	cfg := TurnConfig{LLM: fake, Profile: AgentProfile{SystemPrompt: "t"}, ToolConfig: ToolConfig{Log: LogOrNoop(nil)}}
	RunTurn(context.Background(), cfg, sess, llm.Message{Role: llm.RoleUser, Content: "hi"}, nil)

	if rems := reminderSnapshot(sess); len(rems) != 0 {
		t.Fatalf("whitespace-only Interject produced reminders: %+v", rems)
	}
}

func recordedReminderText(sess *Session) string {
	var texts []string
	for _, reminder := range reminderSnapshot(sess) {
		texts = append(texts, reminder.Text)
	}
	return strings.Join(texts, "\n")
}

func providerCallsContain(calls []fakellm.Call, text string) bool {
	for _, call := range calls {
		for _, msg := range call.Msgs {
			if strings.Contains(msg.Content, text) {
				return true
			}
		}
	}
	return false
}
