package chat

import (
	"context"
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
	"github.com/weatherjean/shell3/internal/runs"
)

func openTestStore(t *testing.T) *runs.Store {
	t.Helper()
	st, err := runs.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return st
}

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
