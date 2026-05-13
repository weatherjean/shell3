package test

import (
	"context"
	"testing"
	"time"

	"github.com/weatherjean/shell3/pkg/applog"
	"github.com/weatherjean/shell3/pkg/chat"
	"github.com/weatherjean/shell3/pkg/hooks"
	"github.com/weatherjean/shell3/pkg/llm"
	"github.com/weatherjean/shell3/pkg/llm/fakellm"
	"github.com/weatherjean/shell3/pkg/persona"
)

// TestLibE2E_SingleTurn exercises the embedder path: construct chat.TurnConfig
// manually (no bootstrap, no FS), run one turn through a fake LLM, verify the
// event stream shape end-to-end.
func TestLibE2E_SingleTurn(t *testing.T) {
	fake := fakellm.New(fakellm.Script{
		Events: []llm.StreamEvent{
			{TextDelta: "hi"},
			{TextDelta: " there"},
			{Usage: &llm.Usage{PromptTokens: 5, CompletionTokens: 2, TotalTokens: 7}},
		},
	})

	sess := chat.NewSession(chat.SessionOpts{BufSize: 64})
	sess.Start(map[string]string{"mode": "test"})

	// Collect events in a goroutine until the channel closes.
	type evRec struct {
		Kind chat.EventKind
		Text string
	}
	var collected []evRec
	done := make(chan struct{})
	go func() {
		for ev := range sess.Events() {
			collected = append(collected, evRec{Kind: ev.Kind, Text: ev.Text})
		}
		close(done)
	}()

	cfg := chat.TurnConfig{
		LLM:         fake,
		Hooks:       hooks.NewRunner(hooks.Config{}),
		Personality: persona.BasePersona("you are a test", nil),
		StatusLine:  "test │ model",
		Log:         applog.Noop{},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sess.Run(ctx, cfg, "say hi")

	sess.End("ok")
	sess.CloseEvents()
	<-done

	// Assert: session_start, user_message, assistant_token+, assistant_message,
	// usage, turn_done, session_end all present.
	kinds := make([]chat.EventKind, 0, len(collected))
	for _, e := range collected {
		kinds = append(kinds, e.Kind)
	}

	want := map[chat.EventKind]int{
		chat.EventSessionStart:     1,
		chat.EventUserMessage:      1,
		chat.EventAssistantToken:   2, // "hi", " there"
		chat.EventAssistantMessage: 1,
		chat.EventUsage:            1,
		chat.EventTurnDone:         1,
		chat.EventSessionEnd:       1,
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
