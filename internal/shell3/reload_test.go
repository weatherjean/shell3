package shell3

import (
	"context"
	"testing"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
)

func sessionConfigIdentity(s *Session) (model, prompt string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cfg.ModelID, s.cfg.Profile.SystemPrompt
}

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
	model, prompt := sessionConfigIdentity(sess)
	if model != "new" || prompt != "new prompt" || len(snap.Tools) != 2 || snap.Tools[1].Name != "telegram" {
		t.Fatalf("reloaded snapshot = %+v", snap)
	}
	created, err := rt.Session(SessionOpts{Name: "after"})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := sessionConfigIdentity(created); got != "new" {
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
	if _, got := sessionConfigIdentity(sess); got != "old" {
		t.Fatalf("busy session changed prompt to %q", got)
	}
	created, err := rt.Session(SessionOpts{Name: "after"})
	if err != nil {
		t.Fatal(err)
	}
	if _, got := sessionConfigIdentity(created); got != "new" {
		t.Fatalf("new session prompt=%q", got)
	}
	cancel()
	for range events {
	}
	if _, got := sessionConfigIdentity(sess); got != "new" {
		t.Fatalf("completed session prompt=%q", got)
	}
}
