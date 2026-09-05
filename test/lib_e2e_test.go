package test

import (
	"context"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
)

func TestLibE2E_SingleTurn(t *testing.T) {
	fake := fakellm.New(fakellm.Script{
		Events: []llm.StreamEvent{
			{TextDelta: "hi"},
			{TextDelta: " there"},
			{Usage: &llm.Usage{PromptTokens: 5, CompletionTokens: 2, TotalTokens: 7}},
		},
	})

	type evRec struct {
		Kind chat.EventKind
		Text string
	}
	var collected []evRec
	sess := chat.NewSession(chat.SessionOpts{Sink: func(ev chat.Event) {
		collected = append(collected, evRec{Kind: ev.Kind, Text: ev.Text})
	}})

	cfg := chat.TurnConfig{
		LLM:        fake,
		Profile:    chat.AgentProfile{SystemPrompt: "you are a test", Tools: nil},
		ModelID:    "model",
		ToolConfig: chat.ToolConfig{Log: applog.Noop{}},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sess.Run(ctx, cfg, "say hi")

	kinds := make([]chat.EventKind, 0, len(collected))
	for _, e := range collected {
		kinds = append(kinds, e.Kind)
	}

	want := map[chat.EventKind]int{
		chat.EventAssistantToken: 2,
		chat.EventUsage:          1,
		chat.EventTurnDone:       1,
	}
	for k, n := range want {
		got := 0
		for _, ek := range kinds {
			if ek == k {
				got++
			}
		}
		if got != n {
			t.Errorf("event %v: got %d, want %d (full sequence: %v)", k, got, n, kinds)
		}
	}

	if fake.CallCount() != 1 {
		t.Errorf("LLM called %d times, want 1", fake.CallCount())
	}
}
