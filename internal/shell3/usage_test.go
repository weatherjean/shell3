package shell3

import (
	"context"
	"testing"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
	"github.com/weatherjean/shell3/internal/persona"
)

func TestSession_AccumulatesUsageAcrossTurns(t *testing.T) {
	mk := func() chat.Config {
		return chat.Config{
			LLM: fakellm.New(fakellm.Script{Events: []llm.StreamEvent{
				{TextDelta: "hi"},
				{Usage: &llm.Usage{PromptTokens: 100, CompletionTokens: 10, TotalTokens: 110}},
			}}),
			ModeLabel: "code",
		}
	}
	rt := newTestRuntime(t, mk)
	s, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		for range s.Send(context.Background(), "hello") {
		}
	}

	got, err := rt.store.SessionMeta(s.ID())
	if err != nil {
		t.Fatal(err)
	}
	if got.TotalPromptTokens != 300 || got.TotalCompletionTokens != 30 {
		t.Fatalf("usage = %d/%d, want 300/30", got.TotalPromptTokens, got.TotalCompletionTokens)
	}
}

func TestSession_AccumulatesUsageWithinMultiRoundTurn(t *testing.T) {
	mk := func() chat.Config {
		return chat.Config{
			LLM: fakellm.New(
				fakellm.Script{Events: []llm.StreamEvent{
					{ToolCall: &llm.ToolCall{ID: "1", Name: "bash", RawArgs: `{"command":"pwd"}`}},
					{Usage: &llm.Usage{PromptTokens: 100, CompletionTokens: 10}},
				}},
				fakellm.Script{Events: []llm.StreamEvent{
					{TextDelta: "done"},
					{Usage: &llm.Usage{PromptTokens: 150, CompletionTokens: 20}},
				}},
			),
			ModeLabel: "code",
			Personality: persona.Persona{Tools: []llm.ToolDefinition{{
				Name: "bash", Parameters: map[string]any{"type": "object"},
			}}},
		}
	}
	rt := newTestRuntime(t, mk)
	s, err := rt.Session(SessionOpts{})
	if err != nil {
		t.Fatal(err)
	}
	for range s.Send(context.Background(), "go") {
	}

	got, err := rt.store.SessionMeta(s.ID())
	if err != nil {
		t.Fatal(err)
	}
	if got.TotalPromptTokens != 250 || got.TotalCompletionTokens != 30 {
		t.Fatalf("usage = %d/%d, want 250/30 (both rounds summed, not just the last)",
			got.TotalPromptTokens, got.TotalCompletionTokens)
	}
}
