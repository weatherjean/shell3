package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/lispconfig"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
	"github.com/weatherjean/shell3/internal/shell3"
)

func TestLispTurnRunsOnlyCoreToolsEndToEnd(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "shell3.lisp")
	src := `(shell3
  (version 1)
  (model primary
    (api-key-env SHELL3_TEST_KEY)
    (id "model-test")
    (reasoning low)
    (context-window 64000))
  (orchestrator
    (model primary)
    (prompt "You are shell3, an agent harness. The transport is only a control surface. Author *.wrk.lisp files containing (task \"checked-change\") and (loop implement). Local test instruction."))
  (memory "Remember the operator preference.")
  (skill weather
    (description "Search current weather.")
    (instructions "Use the weather script.")))`
	if err := os.WriteFile(configPath, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL3_TEST_KEY", "process-value")
	fake := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{
			{ToolCall: &llm.ToolCall{ID: "provider-id", Name: "bash", RawArgs: `{"command":"printf orchestrated"}`}},
		}},
		fakellm.Script{Events: []llm.StreamEvent{{TextDelta: "finished"}}},
	)
	var gotModel lispconfig.Model
	rt, err := openWithClient(context.Background(), configPath, dir, func(m lispconfig.Model, key string) llm.Streamer {
		gotModel = m
		if key != "process-value" {
			t.Fatalf("client key = %q", key)
		}
		return fake
	})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	sess, err := rt.Session(shell3.SessionOpts{Name: "test", Headless: true})
	if err != nil {
		t.Fatal(err)
	}
	if gotModel.ID != "model-test" || gotModel.Reasoning != "low" {
		t.Fatalf("resolved model = %+v", gotModel)
	}
	var result, answer string
	for ev := range sess.Send(context.Background(), "do it") {
		switch ev.Kind {
		case shell3.ToolResult:
			result = ev.ToolOutput
		case shell3.Token:
			answer += ev.Text
		case shell3.Error:
			t.Fatalf("turn error: %v", ev.Err)
		}
	}
	if result != "orchestrated" || answer != "finished" {
		t.Fatalf("result/answer = %q / %q", result, answer)
	}
	call := fake.CallsSnapshot()[0]
	var names []string
	for _, def := range call.Tools {
		names = append(names, def.Name)
	}
	if strings.Join(names, ",") != "bash,bash_bg" {
		t.Fatalf("tools = %v", names)
	}
	if !strings.Contains(call.Msgs[0].Content, "(task \"checked-change\"") ||
		!strings.Contains(call.Msgs[0].Content, "(loop implement") ||
		strings.Contains(call.Msgs[0].Content, "(wrk\n") ||
		!strings.Contains(call.Msgs[0].Content, "shell3, an agent harness") ||
		!strings.Contains(call.Msgs[0].Content, "transport is only a control surface") ||
		!strings.Contains(call.Msgs[0].Content, "*.wrk.lisp") || !strings.Contains(call.Msgs[0].Content, "Local test instruction") ||
		!strings.Contains(call.Msgs[0].Content, "weather: Search current weather.") ||
		!strings.Contains(call.Msgs[0].Content, "Remember the operator preference.") ||
		strings.Contains(call.Msgs[0].Content, "Use the weather script.") {
		t.Fatalf("system prompt missing orchestrator contract: %q", call.Msgs[0].Content)
	}
}

func TestSessionFactoryBuildsIndependentProviderClients(t *testing.T) {
	t.Setenv("SHELL3_TEST_KEY", "process-value")
	cfg := &lispconfig.Config{
		Models: map[string]lispconfig.Model{"primary": {APIKeyEnv: "SHELL3_TEST_KEY", ID: "model-test"}},
		Main:   &lispconfig.Orchestrator{Model: "primary", Prompt: "coordinate"},
	}
	created := 0
	factory, err := sessionFactory(cfg, "/tmp/shell3.lisp", t.TempDir(), false, nil, applog.Noop{}, func(lispconfig.Model, string) llm.Streamer {
		created++
		return fakellm.New()
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := factory(shell3.SessionOpts{Name: "first"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := factory(shell3.SessionOpts{Name: "second"})
	if err != nil {
		t.Fatal(err)
	}
	if created != 2 || first.LLM == second.LLM {
		t.Fatalf("provider clients created = %d, same = %v", created, first.LLM == second.LLM)
	}
}

func TestContextPolicyIsDerivedFromWindow(t *testing.T) {
	got := contextPolicy(100_000)
	if got.ContextWindow != 100_000 || got.CompactAt != 80_000 || got.PruneAt != 48_000 || got.KeepRecent != 0 || got.HostToolNames != nil {
		t.Fatalf("policy = %+v", got)
	}
	got = contextPolicy(0)
	if got.ContextWindow != 0 || got.CompactAt != 0 || got.PruneAt != 0 || got.KeepRecent != 0 || got.HostToolNames != nil {
		t.Fatalf("zero policy = %+v", got)
	}
}

func TestOpenRequiresOrchestratorAndNamedSecret(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "shell3.lisp")
	if err := os.WriteFile(path, []byte(`(shell3 (version 1))`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(context.Background(), path, dir); err == nil || !strings.Contains(err.Error(), "missing orchestrator") {
		t.Fatalf("error = %v", err)
	}

	if err := os.WriteFile(path, []byte(`(shell3 (version 1) (model m (api-key-env DEFINITELY_MISSING_SHELL3_KEY) (id "x")) (orchestrator (model m) (prompt "test")))`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(context.Background(), path, dir); err == nil || !strings.Contains(err.Error(), "required secret DEFINITELY_MISSING_SHELL3_KEY is absent") {
		t.Fatalf("error = %v", err)
	}
}
