package chat

import (
	"context"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
	"github.com/weatherjean/shell3/internal/persona"
)

// TestEmptyInboxSeededTurn_DoesNotPersistEmptyUserMessage proves the wake-turn
// history-hygiene defect: a turn initiated with an empty user message (the
// RunQueued wake turn) seeded purely from the inbox must NOT persist an empty,
// part-less user row. Such a row replays as openai.UserMessage("") on later
// turns, which real providers reject with HTTP 400.
func TestEmptyInboxSeededTurn_DoesNotPersistEmptyUserMessage(t *testing.T) {
	fake := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "ok"}}})
	sess, _ := newCollectorSession(SessionOpts{})
	sess.Interject("do the queued thing")

	cfg := TurnConfig{LLM: fake, Personality: persona.Persona{SystemPrompt: "t"}, Log: LogOrNoop(nil)}
	// Mirror RunQueued: an empty initiating user message; the inbox supplies input.
	RunTurn(context.Background(), cfg, sess, llm.Message{Role: llm.RoleUser, Content: ""}, nil)

	for i, m := range sess.Messages() {
		if m.Role == llm.RoleUser && m.Content == "" && len(m.ContentParts) == 0 {
			t.Fatalf("empty part-less user message persisted at index %d: %+v", i, sess.Messages())
		}
	}
}

// TestEmptyInboxSeededTurn_QueuedTextReachesWire guards the fix's correctness:
// even without an empty carrier message persisted, the queued text must still
// reach the model on the wire (via the inbox-drain reminder injection). If this
// fails, the fix has silently dropped the wake turn's only input.
func TestEmptyInboxSeededTurn_QueuedTextReachesWire(t *testing.T) {
	fake := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "ok"}}})
	sess, _ := newCollectorSession(SessionOpts{})
	sess.Interject("do the queued thing")

	cfg := TurnConfig{LLM: fake, Personality: persona.Persona{SystemPrompt: "t"}, Log: LogOrNoop(nil)}
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
		for _, p := range m.ContentParts {
			if strings.Contains(p.Text, "do the queued thing") {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("queued text did not reach the wire on the wake turn; msgs=%+v", calls[0].Msgs)
	}
}

// TestNormalTurn_PersistsUserMessage guards against over-stripping: a normal
// non-empty turn must still persist its initiating user message.
func TestNormalTurn_PersistsUserMessage(t *testing.T) {
	fake := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "ok"}}})
	sess, _ := newCollectorSession(SessionOpts{})

	cfg := TurnConfig{LLM: fake, Personality: persona.Persona{SystemPrompt: "t"}, Log: LogOrNoop(nil)}
	RunTurn(context.Background(), cfg, sess, llm.Message{Role: llm.RoleUser, Content: "hello"}, nil)

	var found bool
	for _, m := range sess.Messages() {
		if m.Role == llm.RoleUser && m.Content == "hello" {
			found = true
		}
	}
	if !found {
		t.Fatalf("normal turn dropped its user message; msgs=%+v", sess.Messages())
	}
}
