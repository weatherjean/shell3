package chat

import (
	"context"
	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
	"github.com/weatherjean/shell3/internal/runs"
	"testing"
)

func TestCompactInto_NoDuplicateMessages(t *testing.T) {
	st, err := runs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open runs store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	prevID, err := st.NewSession()
	if err != nil {
		t.Fatalf("new session: %v", err)
	}

	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "turn 1 user"},
		{Role: llm.RoleAssistant, Content: "turn 1 assistant"},
		{Role: llm.RoleUser, Content: "turn 2 user"},
	}

	for _, m := range msgs {
		if err := st.AppendMessage(prevID, m); err != nil {
			t.Fatalf("AppendMessage: %v", err)
		}
	}

	sess := NewSession(SessionOpts{StoreID: prevID})
	sess.messages = append(sess.messages, msgs...)
	sess.persistedLen = len(msgs) // high-water mark: all persisted

	compactInto(CompactSummary{Summary: "compacted"}, st, sess, nil, applog.Noop{})

	got, err := st.LoadMessages(prevID)
	if err != nil {
		t.Fatalf("LoadMessages(prevID): %v", err)
	}
	if len(got) != len(msgs) {
		t.Errorf("outgoing session: got %d messages, want %d (duplication bug?)", len(got), len(msgs))
		for i, m := range got {
			t.Logf("  [%d] role=%s content=%q", i, m.Role, m.Content)
		}
	}
	for i := range msgs {
		if i >= len(got) {
			break
		}
		if got[i].Role != msgs[i].Role || got[i].Content != msgs[i].Content {
			t.Errorf("outgoing[%d]: got {%s %q}, want {%s %q}",
				i, got[i].Role, got[i].Content, msgs[i].Role, msgs[i].Content)
		}
	}
}

func TestCompactInto_MirrorsCompactedContextToNewSession(t *testing.T) {
	st, err := runs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open runs store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	id, err := st.NewSession()
	if err != nil {
		t.Fatalf("new session: %v", err)
	}

	sess := NewSession(SessionOpts{StoreID: id})
	sess.messages = []llm.Message{
		{Role: llm.RoleUser, Content: "old 1"},
		{Role: llm.RoleAssistant, Content: "old 2"},
	}

	compactInto(CompactSummary{Summary: "did stuff"}, st, sess, nil, applog.Noop{})

	got, err := st.LoadMessages(sess.id)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != len(sess.messages) {
		t.Fatalf("persisted %d msgs, in-memory %d", len(got), len(sess.messages))
	}
	for i := range got {
		if got[i].Role != sess.messages[i].Role || got[i].Content != sess.messages[i].Content {
			t.Fatalf("seq %d mismatch: %#v vs %#v", i, got[i], sess.messages[i])
		}
	}
}

func TestSaveHistory_AfterCompaction(t *testing.T) {
	st, err := runs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open runs store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	newID, err := st.NewSession()
	if err != nil {
		t.Fatalf("new session: %v", err)
	}

	compactedMsgs := []llm.Message{
		{Role: llm.RoleUser, Content: "<system-reminder>Continuation of session old-id...</system-reminder>"},
		{Role: llm.RoleAssistant, Content: "trigger assistant message"},
	}
	for _, m := range compactedMsgs {
		if err := st.AppendMessage(newID, m); err != nil {
			t.Fatalf("AppendMessage (compacted): %v", err)
		}
	}

	sess := NewSession(SessionOpts{StoreID: newID})
	sess.messages = append(sess.messages, compactedMsgs...)
	sess.persistedLen = len(compactedMsgs)

	thisTurnUser := llm.Message{Role: llm.RoleUser, Content: "this turn's user message"}
	thisTurnAssistant := llm.Message{Role: llm.RoleAssistant, Content: "this turn's assistant reply"}
	sess.messages = append(sess.messages, thisTurnUser, thisTurnAssistant)

	saveHistory(st, applog.Noop{}, sess, newID)

	got, err := st.LoadMessages(newID)
	if err != nil {
		t.Fatalf("LoadMessages: %v", err)
	}

	want := append(append([]llm.Message{}, compactedMsgs...), thisTurnUser, thisTurnAssistant)
	if len(got) != len(want) {
		t.Errorf("got %d messages, want %d", len(got), len(want))
		for i, m := range got {
			t.Logf("  got[%d] role=%s content=%q", i, m.Role, m.Content)
		}
		for i, m := range want {
			t.Logf("  want[%d] role=%s content=%q", i, m.Role, m.Content)
		}
		t.FailNow()
	}

	for i := range want {
		if got[i].Role != want[i].Role || got[i].Content != want[i].Content {
			t.Errorf("messages[%d]: got {%s %q}, want {%s %q}",
				i, got[i].Role, got[i].Content, want[i].Role, want[i].Content)
		}
	}

	if sess.persistedLen != 4 {
		t.Errorf("persistedLen: got %d, want 4", sess.persistedLen)
	}
}

func TestResume_RestoresPersistedPromptTokens_CompactsFirstTurn(t *testing.T) {
	st, err := runs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open runs store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	id, err := st.NewSession()
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

func openTestStore(t *testing.T) *runs.Store {
	t.Helper()
	st, err := runs.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestRun_PersistsHistoryBeforeTurnDone(t *testing.T) {
	st := openTestStore(t)
	sessionID, err := st.NewSession()
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
		LLM:        llmClient,
		Profile:    AgentProfile{SystemPrompt: "test"},
		ToolConfig: ToolConfig{Store: st, Log: LogOrNoop(nil)},
	}

	sess.Run(context.Background(), cfg, "hi there")

	if !sawTurnDone {
		t.Fatal("turn_done never observed")
	}
}
