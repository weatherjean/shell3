package test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/agentsetup"
	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/config"
	"github.com/weatherjean/shell3/internal/kit"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
)

func writeConfigTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// integWiring is the `shell3:` block every kit below opens with.
const integWiring = `#---
# shell3:
#   models:
#     m:
#       base_url: https://api.example.com/v1
#       api_key: sk-test
#       model: x
#---
`

const integAgent = `
#---
# agent: main
# model: m
# use: [bash]
#---
main_prompt() { cat <<'SHELL3_EOF'
you are a test agent
SHELL3_EOF
}
`

func loadKitConfig(t *testing.T, dir, body string, gates, notes map[string]string) *config.LoadedConfig {
	t.Helper()
	writeConfigTree(t, dir, map[string]string{kit.FileName: body})
	lc, err := config.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	lc.SetKitHooks(filepath.Join(dir, kit.FileName), "main", config.KitHooks{Gates: gates, Notes: notes})
	return lc
}

func assembledConfig(t *testing.T, dir string) chat.Config {
	t.Helper()
	parts, cleanup, err := agentsetup.BuildParts(agentsetup.Options{
		ConfigDir: dir,
		CWD:       dir,
		HomeDir:   t.TempDir(),
	})
	if err != nil {
		t.Fatalf("BuildParts: %v", err)
	}
	t.Cleanup(cleanup)
	cfg, err := parts.SessionConfig(agentsetup.SessionOptions{Agent: "main", WorkDir: dir})
	if err != nil {
		t.Fatalf("SessionConfig: %v", err)
	}
	return cfg
}

func TestConfigIntegration_ToolCallHooks(t *testing.T) {
	dir := t.TempDir()
	lc := loadKitConfig(t, dir, integWiring+integAgent+`
#---
# gate: [main]
#---
main_gate() {
  in=$(cat)
  case "$in" in
    *'rm -rf /'*) printf '{"block": true, "reason": "dangerous"}'; exit 0 ;;
  esac
  case "$in" in
    *'"name":"read"'*.env*) printf '{"block": true, "reason": "no reading .env"}'; exit 0 ;;
  esac
  exit 0
}

#---
# note: [main]
#---
main_note() {
  in=$(cat)
  if printf '%s' "$in" | grep -q 'SECRET-TOKEN'; then
    printf '{"output": "[redacted]"}'
  fi
}
`, map[string]string{"main": "main_gate"}, map[string]string{"main": "main_note"})
	assembled := assembledConfig(t, dir)

	ctx := context.Background()

	if !lc.HasToolCall() {
		t.Fatal("expected a tool-call hook to be discovered")
	}
	v := lc.RunToolCall(ctx, "main", "bash", "rm -rf /", "{}", false)
	if v.Action != config.ActionBlock {
		t.Error("tool-call hook should block rm -rf /")
	}
	if !strings.Contains(v.Reason, "dangerous") {
		t.Errorf("reason should mention 'dangerous', got: %q", v.Reason)
	}
	if v2 := lc.RunToolCall(ctx, "main", "bash", "echo hello", "{}", false); v2.Action != config.ActionRun {
		t.Errorf("tool-call hook should allow 'echo hello', got action=%v", v2.Action)
	}

	t.Run("tool_call_blocks_via_turn", func(t *testing.T) {
		events := runToolCallTurn(t, assembled, dir, "run dangerous command",
			llm.ToolCall{ID: "1", Name: "bash", RawArgs: `{"command":"rm -rf /"}`}, nil)
		assertToolResultContains(t, events, "blocked by tool-call hook")
	})

	t.Run("tool_result_redacts_via_turn", func(t *testing.T) {
		events := runToolCallTurn(t, assembled, dir, "echo a secret",
			llm.ToolCall{ID: "1", Name: "bash", RawArgs: `{"command":"echo SECRET-TOKEN"}`},
			nil)

		var resultOut string
		var found bool
		for _, ev := range events {
			if ev.Kind == chat.EventToolResult {
				resultOut, found = ev.ToolOutput, true
			}
		}
		if !found {
			t.Fatal("no tool result event emitted")
		}
		if strings.Contains(resultOut, "SECRET-TOKEN") {
			t.Errorf("tool-result hook did not redact the model-visible output: %q", resultOut)
		}
		if !strings.Contains(resultOut, "[redacted]") {
			t.Errorf("expected redacted marker in tool output, got: %q", resultOut)
		}
	})

	t.Run("non_bash_read_gated_via_turn", func(t *testing.T) {
		events := runToolCallTurn(t, assembled, dir, "read the env file",
			llm.ToolCall{ID: "1", Name: "read", RawArgs: `{"path":".env"}`}, nil)
		assertToolResultContains(t, events, "no reading .env")
	})
}

func runToolCallTurn(t *testing.T, assembled chat.Config, dir, prompt string, tc llm.ToolCall, tweak func(*chat.TurnConfig)) []chat.Event {
	t.Helper()
	fake := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{{ToolCall: &tc}}},
		fakellm.Script{Events: []llm.StreamEvent{
			{TextDelta: "done"},
			{Usage: &llm.Usage{PromptTokens: 5, CompletionTokens: 1, TotalTokens: 6}},
		}},
	)
	toolDefs := config.ToolDefs([]string{"bash", "read"})

	var events []chat.Event
	sess := chat.NewSession(chat.SessionOpts{Sink: func(ev chat.Event) { events = append(events, ev) }})
	assembled.LLM = fake
	assembled.Profile = chat.AgentProfile{SystemPrompt: "you are a test", Tools: toolDefs}
	assembled.ModelID = "x"
	assembled.Log = applog.Noop{}
	assembled.WorkDir = dir
	turnCfg := chat.NewTurnConfig(assembled, chat.NewHandlers())
	if tweak != nil {
		tweak(&turnCfg)
	}

	turnCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	sess.Run(turnCtx, turnCfg, prompt)
	sess.End(chat.StatusOK)
	return events
}

func assertToolResultContains(t *testing.T, events []chat.Event, want string) {
	t.Helper()
	for _, ev := range events {
		if ev.Kind == chat.EventToolResult && strings.Contains(ev.ToolOutput, want) {
			return
		}
	}
	var texts []string
	for _, ev := range events {
		texts = append(texts, ev.Kind.String()+"="+ev.ToolOutput)
	}
	t.Errorf("expected a tool result containing %q; events: %v", want, texts)
}

func TestConfigIntegration_EmptyRewriteOnNonBashFailsClosed(t *testing.T) {
	dir := t.TempDir()
	loadKitConfig(t, dir, integWiring+integAgent+`
#---
# gate: [main]
#---
main_gate() {
  in=$(cat)
  cmd=$(printf '%s' "$in" | sed -n 's/.*"command":"\([^"]*\)".*/\1/p')
  printf '{"command": "%s"}' "$(printf '%s' "$cmd" | sed 's/rm/echo/g')"
}
`, map[string]string{"main": "main_gate"}, nil)

	events := runToolCallTurn(t, assembledConfig(t, dir), dir, "read the readme",
		llm.ToolCall{ID: "1", Name: "read", RawArgs: `{"path":"README.md"}`}, nil)

	var blocked bool
	for _, ev := range events {
		if ev.Kind == chat.EventToolResult && strings.Contains(ev.ToolOutput, "only to bash tools") {
			blocked = true
		}
	}
	if !blocked {
		var texts []string
		for _, ev := range events {
			texts = append(texts, ev.Kind.String()+"="+ev.ToolOutput)
		}
		t.Errorf("expected read blocked (command verdict is bash-only); events: %v", texts)
	}
}

func TestConfigIntegration_PerAgentHooks(t *testing.T) {
	dir := t.TempDir()
	lc := loadKitConfig(t, dir, integWiring+integAgent+`
#---
# agent: explorer
# description: read-only
# model: m
# use: [bash]
#---
explorer_prompt() { cat <<'SHELL3_EOF'
explore
SHELL3_EOF
}

#---
# agent: free
# description: ungated
# model: m
# use: [bash]
#---
free_prompt() { cat <<'SHELL3_EOF'
go
SHELL3_EOF
}

#---
# gate: [main]
#---
main_gate() { printf '{"block": true, "reason": "main-gate"}'; }

#---
# gate: [explorer]
#---
explorer_gate() {
  in=$(cat)
  case "$in" in
    *'"command":"rg'*|*'"command":"cat'*|*'"command":"ls'*) exit 0 ;;
  esac
  printf '{"block": true, "reason": "explorer is read-only"}'
}
`, map[string]string{"main": "main_gate", "explorer": "explorer_gate"}, nil)

	ctx := context.Background()

	if v := lc.RunToolCall(ctx, "main", "bash", "ls", "{}", false); v.Action != config.ActionBlock || v.Reason != "main-gate" {
		t.Errorf("main agent verdict = %+v", v)
	}
	if v := lc.RunToolCall(ctx, "explorer", "bash", "rg foo", "{}", true); v.Action != config.ActionRun {
		t.Errorf("explorer rg verdict = %+v", v)
	}
	if v := lc.RunToolCall(ctx, "explorer", "bash", "git push", "{}", true); v.Action != config.ActionBlock {
		t.Errorf("explorer git push verdict = %+v", v)
	}
	if v := lc.RunToolCall(ctx, "free", "bash", "git push", "{}", true); v.Action != config.ActionRun || !v.Passthrough {
		t.Errorf("free verdict = %+v", v)
	}
}
