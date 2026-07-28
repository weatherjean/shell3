package chat

import (
	"context"
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
	"github.com/weatherjean/shell3/internal/persona"
	"github.com/weatherjean/shell3/internal/runs"
)

// TestResume_RestoresPersistedPromptTokens_CompactsFirstTurn is the Q3
// regression: a session resumed from the store must restore the provider-
// reported prompt-token count (not re-derive the chars/4 estimate), so the
// FIRST resumed turn's maybeCompact fires when the persisted count is already
// over compact_at. Token-dense histories (digits, code, CJK) estimate far below
// their real token cost, so without the persisted count a resumed thread grows
// unbounded past the model window without ever compacting.
func TestResume_RestoresPersistedPromptTokens_CompactsFirstTurn(t *testing.T) {
	st, err := runs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open runs store: %v", err)
	}
	id, err := st.NewSession(runs.Meta{})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}

	// A token-dense history: messages whose chars/4 estimate stays well under
	// CompactAt (so an estimate-driven resume would NOT compact), but which the
	// provider reported at 5000 prompt tokens — the count we persist and must
	// restore. Sized so there is still a real head to summarize (≥ compactionFloor
	// messages once the KeepRecent tail is carved off).
	seed := make([]llm.Message, 0, 24)
	for range 24 {
		seed = append(seed, llm.Message{Role: llm.RoleAssistant, Content: "12345678"}) // ~2 est. tokens each
	}
	for _, m := range seed {
		if err := st.AppendMessage(id, m); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	const persisted = 5000
	if err := st.SetLastPromptTokens(id, persisted); err != nil {
		t.Fatalf("set tokens: %v", err)
	}

	// Sanity: the estimate is far below the persisted count and below CompactAt,
	// so a resume that fell back to the estimate would NOT compact.
	if est := estimatePromptTokens(seed); est >= 100 {
		t.Fatalf("estimate %d ≥ CompactAt; test would not distinguish estimate from persisted count", est)
	}

	// Resume exactly as the front-end wiring does: seed messages + the persisted
	// count read back from the store.
	loaded, err := st.LoadMessages(id)
	if err != nil {
		t.Fatalf("load messages: %v", err)
	}
	sess := NewSession(SessionOpts{
		StoreID:             id,
		Store:               st,
		InitialMessages:     loaded,
		InitialPromptTokens: st.LastPromptTokens(id),
	})
	if sess.lastPromptTokens != persisted {
		t.Fatalf("restored lastPromptTokens = %d, want %d (the persisted provider count)", sess.lastPromptTokens, persisted)
	}

	fake := fakellm.New(
		// call 0: the quiet compaction summary of the head.
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "SUMMARY"}}},
		// call 1: the resumed turn, answered against the compacted history.
		fakellm.Script{Events: []llm.StreamEvent{
			{TextDelta: "ok"},
			{Usage: &llm.Usage{PromptTokens: 5, TotalTokens: 5}},
		}},
	)
	cfg := TurnConfig{
		LLM:         fake,
		Personality: persona.Persona{SystemPrompt: "test"},
		Log:         LogOrNoop(nil),
		AgentKnobs:  AgentKnobs{CompactAt: 100, KeepRecent: 10},
	}

	// Swap the collector sink in so we can assert the compacted event.
	c := &collector{}
	sess.sink = c.sink

	RunTurn(context.Background(), cfg, sess, llm.Message{Role: llm.RoleUser, Content: "next"}, nil)

	if !hasKind(c.all(), EventCompacted) {
		t.Fatal("first resumed turn should have compacted (persisted count 5000 ≥ CompactAt 100)")
	}
}

// TestResume_NoPersistedTokens_FallsBackToEstimate verifies the old-session
// fallback: when no prompt-token count was persisted (0), resume seeds the gauge
// with the chars/4 estimate over the loaded history rather than leaving it at 0.
func TestResume_NoPersistedTokens_FallsBackToEstimate(t *testing.T) {
	seed := []llm.Message{
		{Role: llm.RoleUser, Content: "some earlier user message"},
		{Role: llm.RoleAssistant, Content: "some earlier assistant reply that is a bit longer"},
	}
	sess := NewSession(SessionOpts{
		InitialMessages:     seed,
		InitialPromptTokens: 0, // old session: nothing persisted
	})
	want := estimatePromptTokens(seed)
	if want == 0 {
		t.Fatal("estimate over seed should be non-zero")
	}
	if sess.lastPromptTokens != want {
		t.Fatalf("fallback lastPromptTokens = %d, want estimate %d", sess.lastPromptTokens, want)
	}
}
