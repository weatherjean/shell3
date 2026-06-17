package chat

import (
	"context"
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
	"github.com/weatherjean/shell3/internal/persona"
	"github.com/weatherjean/shell3/internal/runs"
)

// openTestStore opens a fresh file-native runs store in a temp dir, registered
// for cleanup. File-backed so it exercises the real append path.
func openTestStore(t *testing.T) *runs.Store {
	t.Helper()
	st, err := runs.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// TestRun_PersistsHistoryBeforeTurnDone pins the ordering invariant that the
// turn's messages are persisted to the store *before* the turn_done event is
// emitted.
//
// Why it matters: turn_done is the signal embedders (pkg/shell3, the TUI) use
// to decide a turn is finished and that mutating session state — Clear,
// Rollback → SetMessages — is now safe. saveHistory reads sess.messages. If
// turn_done fired first, an embedder reacting to it could write sess.messages
// concurrently with saveHistory's read: a data race.
//
// The sink is invoked synchronously inside Run, so observing turn_done from it
// means everything Run did before emitting it — including the saveHistory in
// beforeDone — has already happened. The assertion runs right there.
func TestRun_PersistsHistoryBeforeTurnDone(t *testing.T) {
	st := openTestStore(t)
	sessionID, err := st.NewSession(runs.Meta{})
	if err != nil {
		t.Fatal(err)
	}

	llmClient := fakellm.New(fakellm.Script{
		Events: []llm.StreamEvent{
			{TextDelta: "hello"},
			{Usage: &llm.Usage{PromptTokens: 5, CompletionTokens: 2, TotalTokens: 7}},
		},
	})

	var sawTurnDone bool
	sink := func(ev Event) {
		if ev.Kind != EventTurnDone {
			return
		}
		sawTurnDone = true
		// Verify that messages have been persisted before turn_done fires.
		msgs, err := st.LoadMessages(sessionID)
		if err != nil {
			t.Errorf("LoadMessages: %v", err)
			return
		}
		if len(msgs) == 0 {
			t.Errorf("history not persisted when turn_done was observed: " +
				"turn_done fired before saveHistory ran")
		}
	}

	sess := NewSession(SessionOpts{StoreID: sessionID, Sink: sink})
	cfg := TurnConfig{
		LLM:         llmClient,
		Personality: persona.Persona{SystemPrompt: "test"},
		Store:       st,
		Log:         LogOrNoop(nil),
	}

	sess.Run(context.Background(), cfg, "hi there")

	if !sawTurnDone {
		t.Fatal("turn_done never observed")
	}
}
