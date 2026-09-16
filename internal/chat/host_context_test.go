package chat

import (
	"context"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
)

func TestHostContextRefreshesAfterToolAndBetweenTurns(t *testing.T) {
	fake := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{{ToolCall: &llm.ToolCall{ID: "a", Name: "shell3", RawArgs: `{"action":"restart"}`}}}},
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "queued"}}},
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "restarted"}}},
	)
	state := "host-before"
	sess, _ := newCollectorSession(SessionOpts{})
	cfg := TurnConfig{LLM: fake, Profile: AgentProfile{SystemPrompt: "sys", Tools: []llm.ToolDefinition{{Name: "shell3"}}}, ToolConfig: ToolConfig{Log: LogOrNoop(nil)}, AgentKnobs: AgentKnobs{HostContext: func() string { return state }, HostToolNames: map[string]bool{"shell3": true}}, HostTool: func(context.Context, string, string) (string, error) {
		state = "host-queued"
		return `{"restart":"queued"}`, nil
	}}
	RunTurn(t.Context(), cfg, sess, llm.Message{Role: llm.RoleUser, Content: "restart"}, nil)
	state = "host-completed"
	RunTurn(t.Context(), cfg, sess, llm.Message{Role: llm.RoleUser, Content: "did it work?"}, nil)
	calls := fake.CallsSnapshot()
	if len(calls) != 3 {
		t.Fatalf("calls=%d", len(calls))
	}
	for i, want := range []string{"host-before", "host-queued", "host-completed"} {
		if calls[i].Msgs[0].Role != llm.RoleSystem || !strings.Contains(calls[i].Msgs[0].Content, want) || strings.Count(calls[i].Msgs[0].Content, "host-") != 1 {
			t.Fatalf("stale system context: %+v", calls[i].Msgs[0])
		}
		if err := llm.ValidateToolOrder(calls[i].Msgs); err != nil {
			t.Fatal(err)
		}
	}
	for _, m := range sess.messages {
		if strings.Contains(m.Content, "host-") {
			t.Fatal("snapshot accumulated in transcript")
		}
	}
}
