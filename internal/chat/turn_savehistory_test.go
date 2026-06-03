package chat

import (
	"context"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
	"github.com/weatherjean/shell3/internal/persona"
)

// TestRun_PersistsHistoryBeforeTurnDone pins the ordering invariant that the
// turn's messages are persisted to the store *before* the turn_done event is
// emitted.
//
// Why it matters: turn_done is the signal embedders (pkg/shell3, the TUI) use
// to decide a turn is finished and that mutating session state — Clear,
// Rollback → SetMessages — is now safe. saveHistory reads sess.messages. If
// turn_done fires first, an embedder reacting to it can write sess.messages
// concurrently with saveHistory's read: a data race.
//
// A channel receive happens-after the send, so if Run persists before emitting
// turn_done, a consumer that observes turn_done is guaranteed to see the
// persisted rows. This makes the post-fix behavior deterministic.
func TestRun_PersistsHistoryBeforeTurnDone(t *testing.T) {
	st := openTestStore(t)
	sessionID, err := st.StartSession()
	if err != nil {
		t.Fatal(err)
	}

	llmClient := fakellm.New(fakellm.Script{
		Events: []llm.StreamEvent{
			{TextDelta: "hello"},
			{Usage: &llm.Usage{PromptTokens: 5, CompletionTokens: 2, TotalTokens: 7}},
		},
	})

	sess := NewSession(SessionOpts{BufSize: 256, StoreID: sessionID})
	cfg := TurnConfig{
		LLM:         llmClient,
		Personality: persona.Persona{SystemPrompt: "test"},
		Store:       st,
		Log:         LogOrNoop(nil),
	}

	done := make(chan struct{})
	go func() {
		sess.Run(context.Background(), cfg, "hi there")
		close(done)
	}()

	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-sess.Events():
			if ev.Kind != EventTurnDone {
				continue
			}
			res, err := st.HistoryGet(sessionID, 0)
			if err != nil {
				t.Fatalf("HistoryGet: %v", err)
			}
			if len(res.Turns) == 0 {
				t.Fatalf("history not persisted when turn_done was observed: " +
					"turn_done fired before saveHistory ran")
			}
			<-done // let Run finish before the store is closed in cleanup
			return
		case <-deadline:
			t.Fatal("timed out waiting for turn_done")
		}
	}
}
