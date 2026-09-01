//go:build unix

package shell3

import (
	"context"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
)

func TestReloadUpdatesContextWindowForLiveSession(t *testing.T) {
	initial := func() chat.Config {
		return chat.Config{
			LLM: fakellm.New(fakellm.Script{Events: []llm.StreamEvent{
				{TextDelta: "first"},
				{Usage: &llm.Usage{PromptTokens: 400, TotalTokens: 400}},
			}}),
			Agent: "code", ModelID: "model", AgentKnobs: chat.AgentKnobs{ContextWindow: 1000},
		}
	}
	rt := newTestRuntime(t, initial)
	s, err := rt.Session(SessionOpts{Name: "live"})
	if err != nil {
		t.Fatal(err)
	}
	for range s.Send(context.Background(), "first") {
	}

	reloaded := func() chat.Config {
		return chat.Config{
			LLM:   fakellm.New(fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "second"}}}),
			Agent: "code", ModelID: "model", AgentKnobs: chat.AgentKnobs{ContextWindow: 500},
		}
	}
	if _, err := rt.applyReload(fakeReloadState(rt, reloaded, func() {})); err != nil {
		t.Fatal(err)
	}

	var reminder string
	for ev := range s.Send(context.Background(), "second") {
		if ev.Kind == SystemReminder {
			reminder += ev.Text
		}
	}
	if !strings.Contains(reminder, "context: 400 / 500 tokens (80%)") {
		t.Fatalf("reloaded context window did not reach the next live turn: %q", reminder)
	}
}
