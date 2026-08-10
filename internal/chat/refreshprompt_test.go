package chat

import (
	"context"
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
	"github.com/weatherjean/shell3/internal/persona"
)

// The system prompt is re-rendered at every turn when RefreshPrompt is wired:
// a long-lived session must see current context-file contents, not the
// session-creation snapshot. Nil RefreshPrompt (and an empty render) keep the
// construction-time prompt.
func TestRefreshPromptRerendersPerTurn(t *testing.T) {
	fake := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "a1"}}},
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "a2"}}},
	)
	current := "prompt v1"
	cfg := TurnConfig{
		LLM:           fake,
		Personality:   persona.Persona{SystemPrompt: "stale snapshot"},
		RefreshPrompt: func() string { return current },
		Log:           LogOrNoop(nil),
		AgentKnobs:    AgentKnobs{ContextWindow: 4096},
	}
	sess, _ := newCollectorSession(SessionOpts{})

	RunTurn(context.Background(), cfg, sess, llm.Message{Role: llm.RoleUser, Content: "q1"}, nil)
	current = "prompt v2" // the config dir changed between turns
	RunTurn(context.Background(), cfg, sess, llm.Message{Role: llm.RoleUser, Content: "q2"}, nil)

	calls := fake.CallsSnapshot()
	if len(calls) != 2 {
		t.Fatalf("model calls = %d, want 2", len(calls))
	}
	if got := calls[0].Msgs[0].Content; got != "prompt v1" {
		t.Fatalf("turn 1 system prompt = %q, want the refreshed render", got)
	}
	if got := calls[1].Msgs[0].Content; got != "prompt v2" {
		t.Fatalf("turn 2 system prompt = %q — the re-render did not track the change", got)
	}
}

// A nil RefreshPrompt — and a refresher that renders empty — keep the
// construction-time Personality prompt.
func TestRefreshPromptNilOrEmptyKeepsSnapshot(t *testing.T) {
	for name, refresh := range map[string]func() string{
		"nil":   nil,
		"empty": func() string { return "" },
	} {
		fake := fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "a"}}})
		cfg := TurnConfig{
			LLM:           fake,
			Personality:   persona.Persona{SystemPrompt: "construction prompt"},
			RefreshPrompt: refresh,
			Log:           LogOrNoop(nil),
			AgentKnobs:    AgentKnobs{ContextWindow: 4096},
		}
		sess, _ := newCollectorSession(SessionOpts{})
		RunTurn(context.Background(), cfg, sess, llm.Message{Role: llm.RoleUser, Content: "q"}, nil)
		calls := fake.CallsSnapshot()
		if len(calls) != 1 || calls[0].Msgs[0].Content != "construction prompt" {
			t.Fatalf("%s: system prompt should stay the snapshot, got %+v", name, calls[0].Msgs[0])
		}
	}
}
