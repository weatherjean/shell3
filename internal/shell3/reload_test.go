package shell3

import (
	"context"
	"testing"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
)

func TestReloadConfigReplacesIdleSessionsAndPreservesHostTools(t *testing.T) {
	client := fakellm.New()
	factory := func(model, prompt string) func(SessionOpts) (chat.Config, error) {
		return func(SessionOpts) (chat.Config, error) {
			return chat.Config{
				LLM: client, ModelID: model,
				Profile: chat.AgentProfile{SystemPrompt: prompt, Tools: []llm.ToolDefinition{{Name: "bash"}}},
			}, nil
		}
	}
	rt, err := NewConfiguredRuntime(context.Background(), t.TempDir(), nil, 1, nil, factory("old", "old prompt"))
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	sess, err := rt.Session(SessionOpts{Name: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.RegisterHostTool(HostTool{Name: "telegram", Handler: func(context.Context, string) (string, error) { return "ok", nil }}); err != nil {
		t.Fatal(err)
	}
	if err := rt.ReloadConfig(factory("new", "new prompt")); err != nil {
		t.Fatal(err)
	}
	snap := sess.Snapshot()
	if snap.Model != "new" || snap.SystemPrompt != "new prompt" || len(snap.Tools) != 2 || snap.Tools[1].Name != "telegram" {
		t.Fatalf("reloaded snapshot = %+v", snap)
	}
	created, err := rt.Session(SessionOpts{Name: "after"})
	if err != nil {
		t.Fatal(err)
	}
	if got := created.Snapshot().Model; got != "new" {
		t.Fatalf("new session model=%q", got)
	}
}

func TestReloadConfigDefersBusySessionUntilTurnEnds(t *testing.T) {
	client := fakellm.NewBlocking()
	factory := func(prompt string) func(SessionOpts) (chat.Config, error) {
		return func(SessionOpts) (chat.Config, error) {
			return chat.Config{LLM: client, Profile: chat.AgentProfile{SystemPrompt: prompt}}, nil
		}
	}
	rt, err := NewConfiguredRuntime(context.Background(), t.TempDir(), nil, 1, nil, factory("old"))
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	sess, err := rt.Session(SessionOpts{Name: "main"})
	if err != nil {
		t.Fatal(err)
	}
	turnCtx, cancel := context.WithCancel(context.Background())
	events := sess.Send(turnCtx, "hold this turn")
	<-client.Started
	if err := rt.ReloadConfig(factory("new")); err != nil {
		t.Fatal(err)
	}
	if got := sess.Snapshot().SystemPrompt; got != "old" {
		t.Fatalf("busy session changed prompt to %q", got)
	}
	created, err := rt.Session(SessionOpts{Name: "after"})
	if err != nil {
		t.Fatal(err)
	}
	if got := created.Snapshot().SystemPrompt; got != "new" {
		t.Fatalf("new session prompt=%q", got)
	}
	cancel()
	for range events {
	}
	if got := sess.Snapshot().SystemPrompt; got != "new" {
		t.Fatalf("completed session prompt=%q", got)
	}
}
