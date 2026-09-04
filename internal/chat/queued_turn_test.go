package chat

import (
	"context"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
)

func TestEmptyInboxSeededTurn_DoesNotPersistEmptyUserMessage(t *testing.T) {
	fake := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "ok"}}})
	sess, _ := newCollectorSession(SessionOpts{})
	sess.Interject("do the queued thing")

	cfg := TurnConfig{LLM: fake, Profile: AgentProfile{SystemPrompt: "t"}, ToolConfig: ToolConfig{Log: LogOrNoop(nil)}}
	RunTurn(context.Background(), cfg, sess, llm.Message{Role: llm.RoleUser, Content: ""}, nil)

	for i, m := range sess.messages {
		if m.Role == llm.RoleUser && m.Content == "" {
			t.Fatalf("empty user message persisted at index %d: %+v", i, sess.messages)
		}
	}
}

func TestEmptyInboxSeededTurn_QueuedTextReachesWire(t *testing.T) {
	fake := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "ok"}}})
	sess, _ := newCollectorSession(SessionOpts{})
	sess.Interject("do the queued thing")

	cfg := TurnConfig{LLM: fake, Profile: AgentProfile{SystemPrompt: "t"}, ToolConfig: ToolConfig{Log: LogOrNoop(nil)}}
	RunTurn(context.Background(), cfg, sess, llm.Message{Role: llm.RoleUser, Content: ""}, nil)

	calls := fake.CallsSnapshot()
	if len(calls) == 0 {
		t.Fatal("no LLM call recorded")
	}
	var found bool
	for _, m := range calls[0].Msgs {
		if strings.Contains(m.Content, "do the queued thing") {
			found = true
		}
	}
	if !found {
		t.Fatalf("queued text did not reach the wire on the follow-up turn; msgs=%+v", calls[0].Msgs)
	}
}

func TestWhitespaceOnlyInboxSeededTurn_NoProviderCall(t *testing.T) {
	fake := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "ok"}}})
	sess, _ := newCollectorSession(SessionOpts{})
	sess.Interject("   \n\t  ")

	cfg := TurnConfig{LLM: fake, Profile: AgentProfile{SystemPrompt: "t"}, ToolConfig: ToolConfig{Log: LogOrNoop(nil)}}
	RunTurn(context.Background(), cfg, sess, llm.Message{Role: llm.RoleUser, Content: ""}, nil)

	if calls := fake.CallsSnapshot(); len(calls) != 0 {
		t.Fatalf("whitespace-only queued turn sent %d provider call(s); want 0: %+v", len(calls), calls)
	}
	for i, m := range sess.messages {
		if m.Role == llm.RoleUser && m.Content == "" {
			t.Fatalf("empty user message persisted at index %d: %+v", i, sess.messages)
		}
	}
}

func TestNormalTurn_PersistsUserMessage(t *testing.T) {
	fake := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "ok"}}})
	sess, _ := newCollectorSession(SessionOpts{})

	cfg := TurnConfig{LLM: fake, Profile: AgentProfile{SystemPrompt: "t"}, ToolConfig: ToolConfig{Log: LogOrNoop(nil)}}
	RunTurn(context.Background(), cfg, sess, llm.Message{Role: llm.RoleUser, Content: "hello"}, nil)

	var found bool
	for _, m := range sess.messages {
		if m.Role == llm.RoleUser && m.Content == "hello" {
			found = true
		}
	}
	if !found {
		t.Fatalf("normal turn dropped its user message; msgs=%+v", sess.messages)
	}
}
