package chat

import (
	"context"
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
	"github.com/weatherjean/shell3/internal/runs"
)

func TestResume_RestoresPersistedPromptTokens_CompactsFirstTurn(t *testing.T) {
	st, err := runs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open runs store: %v", err)
	}
	id, err := st.NewSession(runs.Meta{})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}

	seed := make([]llm.Message, 0, 24)
	for range 24 {
		seed = append(seed, llm.Message{Role: llm.RoleAssistant, Content: "12345678"})
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

	if est := estimatePromptTokens(seed); est >= 100 {
		t.Fatalf("estimate %d ≥ CompactAt; test would not distinguish estimate from persisted count", est)
	}

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
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "SUMMARY"}}},
		fakellm.Script{Events: []llm.StreamEvent{
			{TextDelta: "ok"},
			{Usage: &llm.Usage{PromptTokens: 5, TotalTokens: 5}},
		}},
	)
	cfg := TurnConfig{
		LLM:        fake,
		Profile:    AgentProfile{SystemPrompt: "test"},
		AgentKnobs: AgentKnobs{CompactAt: 100, KeepRecent: 10},
		ToolConfig: ToolConfig{Log: LogOrNoop(nil)},
	}

	c := &collector{}
	sess.sink = c.sink

	RunTurn(context.Background(), cfg, sess, llm.Message{Role: llm.RoleUser, Content: "next"}, nil)

	if !hasKind(c.all(), EventCompacted) {
		t.Fatal("first resumed turn should have compacted (persisted count 5000 ≥ CompactAt 100)")
	}
}

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
