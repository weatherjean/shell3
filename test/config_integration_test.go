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
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
	"github.com/weatherjean/shell3/internal/persona"
)

// writeConfigTree writes a config tree (path → content, relative, subdirs
// created) into dir.
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

const integYAML = `models:
  m:
    base_url: https://api.example.com/v1
    api_key: sk-test
    model: x
`

// TestConfigIntegration_ToolCallHooks loads a config tree with a tool-call +
// tool-result hook and drives full chat turns through the turn loop using
// fakellm, asserting the hook scripts fire on the real dispatch path (block,
// redact, non-bash gating).
func TestConfigIntegration_ToolCallHooks(t *testing.T) {
	dir := t.TempDir()
	writeConfigTree(t, dir, map[string]string{
		"shell3.yaml": integYAML,
		"agent.md":    "---\nmodel: m\ntools: [bash]\n---\nyou are a test agent\n",
		// The gate: block rm -rf /, block reads of .env by args, else pass.
		"hooks/tool-call.sh": `
in=$(cat)
case "$in" in
  *'rm -rf /'*) printf '{"block": true, "reason": "dangerous"}'; exit 0 ;;
esac
case "$in" in
  *'"name":"read"'*.env*) printf '{"block": true, "reason": "no reading .env"}'; exit 0 ;;
esac
exit 0
`,
		// The redactor: strip SECRET-TOKEN from every tool's output.
		"hooks/tool-result.sh": `
in=$(cat)
if printf '%s' "$in" | grep -q 'SECRET-TOKEN'; then
  printf '{"output": "[redacted]"}'
fi
`,
	})

	lc, err := config.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer lc.Close()

	ctx := context.Background()

	// Hook sanity outside the turn loop: rm -rf / blocked, echo allowed.
	if !lc.HasToolCall() {
		t.Fatal("expected a tool-call hook to be discovered")
	}
	v := lc.RunToolCall(ctx, "agent", "bash", "rm -rf /", "{}", false)
	if v.Action != config.ActionBlock {
		t.Error("tool-call hook should block rm -rf /")
	}
	if !strings.Contains(v.Reason, "dangerous") {
		t.Errorf("reason should mention 'dangerous', got: %q", v.Reason)
	}
	if v2 := lc.RunToolCall(ctx, "agent", "bash", "echo hello", "{}", false); v2.Action != config.ActionRun {
		t.Errorf("tool-call hook should allow 'echo hello', got action=%v", v2.Action)
	}

	// --- Turn 1: the hook blocks the dangerous bash call via the dispatch loop ---
	t.Run("tool_call_blocks_via_turn", func(t *testing.T) {
		events := runToolCallTurn(t, lc, dir, "run dangerous command",
			llm.ToolCall{ID: "1", Name: "bash", RawArgs: `{"command":"rm -rf /"}`}, nil)
		assertToolResultContains(t, events, "blocked by tool-call hook")
	})

	// --- Turn 2: the tool-result hook redacts the model-visible bash output ---
	t.Run("tool_result_redacts_via_turn", func(t *testing.T) {
		events := runToolCallTurn(t, lc, dir, "echo a secret",
			llm.ToolCall{ID: "1", Name: "bash", RawArgs: `{"command":"echo SECRET-TOKEN"}`},
			func(tc *chat.TurnConfig) {
				tc.RunToolResult = func(ctx context.Context, name, argsJSON, output string) string {
					return lc.RunToolResult(ctx, "agent", name, argsJSON, output)
				}
			})

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

	// --- Turn 3: the hook gates a NON-bash tool (read) via the dispatch loop ---
	// Proves the hook fires for every tool with its real name (here a read of
	// .env, blocked by name+args), not just the bash family.
	t.Run("non_bash_read_gated_via_turn", func(t *testing.T) {
		events := runToolCallTurn(t, lc, dir, "read the env file",
			llm.ToolCall{ID: "1", Name: "read", RawArgs: `{"path":".env"}`}, nil)
		assertToolResultContains(t, events, "no reading .env")
	})
}

// runToolCallTurn drives one scripted turn against lc's loaded config: the
// fake LLM issues tc, the dispatch loop runs it (tool-call hook included),
// and a second script ends the turn. tweak, when non-nil, adjusts the
// TurnConfig before the run. Returns every event the session emitted.
func runToolCallTurn(t *testing.T, lc *config.LoadedConfig, dir, prompt string, tc llm.ToolCall, tweak func(*chat.TurnConfig)) []chat.Event {
	t.Helper()
	fake := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{{ToolCall: &tc}}},
		fakellm.Script{Events: []llm.StreamEvent{
			{TextDelta: "done"},
			{Usage: &llm.Usage{PromptTokens: 5, CompletionTokens: 1, TotalTokens: 6}},
		}},
	)
	a := lc.FirstAgent()
	toolDefs := config.ToolDefs(a.Gates)

	var events []chat.Event
	sess := chat.NewSession(chat.SessionOpts{Sink: func(ev chat.Event) { events = append(events, ev) }})
	sess.Start(map[string]string{"mode": "test"})

	turnCfg := chat.TurnConfig{
		LLM:         fake,
		Personality: persona.Persona{Name: "base", SystemPrompt: "you are a test", Tools: toolDefs},
		StatusLine:  "test │ x",
		Log:         applog.Noop{},
		ToolConfig: chat.ToolConfig{
			WorkDir: dir,
			RunToolCall: func(ctx context.Context, name, command, argsJSON string, headless bool) chat.ToolCallVerdict {
				return agentsetup.BridgeVerdict(lc.RunToolCall(ctx, "agent", name, command, argsJSON, headless))
			},
		},
		Handlers: chat.NewHandlers(),
	}
	if tweak != nil {
		tweak(&turnCfg)
	}

	turnCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	sess.Run(turnCtx, turnCfg, prompt)
	sess.End(chat.StatusOK)
	return events
}

// assertToolResultContains fails unless some tool-result event's output
// contains want; on failure it renders the full event list for debugging.
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

// TestConfigIntegration_EmptyRewriteOnNonBashFailsClosed drives the exact
// footgun the Passthrough signal exists to catch: a bash-oriented rewrite hook
// written WITHOUT a name guard, emitting {"command": ""} for a non-bash tool
// (its payload command is null). The resulting argv (["bash","-c",""]) is
// byte-identical to a pure pass; it must fail closed (the non-bash tool is
// blocked), not run, honoring the "command/argv verdicts are bash-only"
// invariant end-to-end through the real dispatch loop.
func TestConfigIntegration_EmptyRewriteOnNonBashFailsClosed(t *testing.T) {
	dir := t.TempDir()
	writeConfigTree(t, dir, map[string]string{
		"shell3.yaml": integYAML,
		"agent.md":    "---\nmodel: m\ntools: [bash]\n---\np\n",
		// No name guard on purpose: always emit a command rewrite, even for a
		// non-bash tool whose payload command is null.
		"hooks/tool-call.sh": `
in=$(cat)
cmd=$(printf '%s' "$in" | sed -n 's/.*"command":"\([^"]*\)".*/\1/p')
printf '{"command": "%s"}' "$(printf '%s' "$cmd" | sed 's/rm/echo/g')"
`,
	})

	lc, err := config.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer lc.Close()

	events := runToolCallTurn(t, lc, dir, "read the readme",
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

// TestConfigIntegration_PerAgentHooks proves the per-agent split end-to-end:
// the explorer subagent's own hook governs its calls, and a subagent with no
// hook file runs ungated even when the main hook would block.
func TestConfigIntegration_PerAgentHooks(t *testing.T) {
	dir := t.TempDir()
	writeConfigTree(t, dir, map[string]string{
		"shell3.yaml":        integYAML,
		"agent.md":           "---\nmodel: m\ntools: [bash]\n---\np\n",
		"agents/explorer.md": "---\ndescription: read-only\ntools: [bash]\n---\nexplore\n",
		"agents/free.md":     "---\ndescription: ungated\ntools: [bash]\n---\ngo\n",
		"hooks/tool-call.sh": `printf '{"block": true, "reason": "main-gate"}'`,
		"hooks/explorer.tool-call.sh": `
in=$(cat)
case "$in" in
  *'"command":"rg'*|*'"command":"cat'*|*'"command":"ls'*) exit 0 ;;
esac
printf '{"block": true, "reason": "explorer is read-only"}'
`,
	})

	lc, err := config.Load(dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer lc.Close()
	ctx := context.Background()

	// Main agent: blocked by its own gate.
	if v := lc.RunToolCall(ctx, "agent", "bash", "ls", "{}", false); v.Action != config.ActionBlock || v.Reason != "main-gate" {
		t.Errorf("main agent verdict = %+v", v)
	}
	// Explorer: its own allowlist governs — rg passes, git push blocked.
	if v := lc.RunToolCall(ctx, "explorer", "bash", "rg foo", "{}", true); v.Action != config.ActionRun {
		t.Errorf("explorer rg verdict = %+v", v)
	}
	if v := lc.RunToolCall(ctx, "explorer", "bash", "git push", "{}", true); v.Action != config.ActionBlock {
		t.Errorf("explorer git push verdict = %+v", v)
	}
	// free has no hook file: ungated, even though the main hook blocks all.
	if v := lc.RunToolCall(ctx, "free", "bash", "git push", "{}", true); v.Action != config.ActionRun || !v.Passthrough {
		t.Errorf("free verdict = %+v", v)
	}
}
