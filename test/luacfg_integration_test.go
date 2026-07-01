package test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
	"github.com/weatherjean/shell3/internal/luacfg"
	"github.com/weatherjean/shell3/internal/persona"
)

// writeTmpFile writes content to dir/name and returns the full path.
func writeTmpFile(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// bridgeVerdict mirrors agentsetup.bridgeToolCallAction: an explicit, fail-closed
// luacfg→chat mapping (any unmapped action → Block) that carries every field,
// including Passthrough. These integration tests wire on_tool_call through this
// rather than a raw chat.ToolCallAction(v.Action) cast, so they exercise the same
// fail-closed boundary — and the same Passthrough plumbing — the production bridge
// guarantees, instead of relying on the two iota blocks happening to align.
func bridgeVerdict(v luacfg.ToolCallVerdict) chat.ToolCallVerdict {
	action := chat.Block // fail closed on any unmapped action
	switch v.Action {
	case luacfg.ActionRun:
		action = chat.Run
	case luacfg.ActionAsk:
		action = chat.Ask
	}
	return chat.ToolCallVerdict{
		Action:      action,
		Argv:        v.Argv,
		Prompt:      v.Prompt,
		Reason:      v.Reason,
		AskTimeout:  v.AskTimeout,
		Passthrough: v.Passthrough,
	}
}

// TestLuacfgIntegration_OnToolCallAndCustomTool loads a luacfg config and drives a
// full chat turn through the chat turn loop using fakellm. It asserts:
//   - A custom (bash command-template) tool call runs its command with the
//     declared param exported into the env and returns the command's stdout.
//   - A bash call with a dangerous command is blocked by shell3.on_tool_call
//     (the gate engine) before it ever executes.
func TestLuacfgIntegration_OnToolCallAndCustomTool(t *testing.T) {
	dir := t.TempDir()
	writeTmpFile(t, dir, "shell3.lua", `
shell3.model("m", {
  base_url = "https://api.example.com/v1",
  api_key  = "sk-test",
  model    = "x",
})

local greet = shell3.tool({
  name        = "greet",
  description = "Say hello to a name.",
  parameters  = {
    type       = "object",
    properties = { name = { type = "string" } },
    required   = { "name" },
  },
  command = [[printf 'hello, %s' "$name"]],
})

shell3.on_tool_call(function(t)
  -- bash family: gate the command. Guard required — t.command is nil for non-bash.
  if t.name == "bash" or t.name == "bash_bg" or t.name == "shell_interactive" then
    if shell3.regex([[(?s)rm\s+-rf\s+/]]):match(t.command) then
      return { block = true, reason = "dangerous" }
    end
  end
  -- non-bash example: refuse to read the .env file (gate by name + args).
  if t.name == "read" and shell3.regex([[\.env]]):match(t.args) then
    return { block = true, reason = "no reading .env" }
  end
end)

shell3.on_tool_result(function(r)
  return { output = (r.output:gsub("SECRET%-TOKEN", "[redacted]")) }
end)

shell3.agent({
  name  = "test-agent",
  model = "m",
  prompt = "you are a test agent",
  tools = {
    bash   = true,
    custom = { greet },
  },
})
`)

	lc, err := luacfg.Load(dir+"/shell3.lua", dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer lc.Close()

	ctx := context.Background()

	// Resolution: greet should export name=test into the env for its command.
	rc, err := lc.ResolveCustomCall("greet", `{"name":"test"}`)
	if err != nil {
		t.Fatalf("ResolveCustomCall: %v", err)
	}
	if rc.Command != `printf 'hello, %s' "$name"` {
		t.Errorf("ResolveCustomCall command: got %q", rc.Command)
	}
	var sawName bool
	for _, e := range rc.Env {
		if e == "name=test" {
			sawName = true
		}
	}
	if !sawName {
		t.Errorf("ResolveCustomCall env missing name=test: %v", rc.Env)
	}

	// on_tool_call closure: rm -rf / should be blocked, echo allowed.
	if !lc.HasToolCall() {
		t.Fatal("expected an on_tool_call hook to be declared")
	}
	v := lc.RunToolCall(ctx, "bash", "rm -rf /", "{}")
	if v.Action != luacfg.ActionBlock {
		t.Error("on_tool_call should block rm -rf /")
	}
	if !strings.Contains(v.Reason, "dangerous") {
		t.Errorf("on_tool_call reason should mention 'dangerous', got: %q", v.Reason)
	}
	v2 := lc.RunToolCall(ctx, "bash", "echo hello", "{}")
	if v2.Action != luacfg.ActionRun {
		t.Errorf("on_tool_call should allow 'echo hello', got action=%v", v2.Action)
	}

	// --- Turn 1: custom tool call path ---
	t.Run("custom_tool_via_turn", func(t *testing.T) {
		fake := fakellm.New(
			fakellm.Script{Events: []llm.StreamEvent{
				{ToolCall: &llm.ToolCall{ID: "1", Name: "greet", RawArgs: `{"name":"world"}`}},
			}},
			fakellm.Script{Events: []llm.StreamEvent{
				{TextDelta: "done"},
				{Usage: &llm.Usage{PromptTokens: 5, CompletionTokens: 1, TotalTokens: 6}},
			}},
		)

		a := lc.FirstAgent()
		customDefs := lc.CustomToolsFor(a.CustomTools)
		toolDefs := luacfg.ToolDefs(a.Gates, customDefs)

		var events []chat.Event
		sess := chat.NewSession(chat.SessionOpts{Sink: func(ev chat.Event) {
			events = append(events, ev)
		}})
		sess.Start(map[string]string{"mode": "test"})

		turnCfg := chat.TurnConfig{
			LLM:               fake,
			Personality:       persona.BasePersona("you are a test", toolDefs),
			StatusLine:        "test │ x",
			WorkDir:           dir,
			Log:               applog.Noop{},
			ResolveCustomTool: lc.ResolveCustomCall,
			CustomToolNames:   map[string]bool{"greet": true},
			RunToolCall: func(ctx context.Context, name, command, argsJSON string) chat.ToolCallVerdict {
				return bridgeVerdict(lc.RunToolCall(ctx, name, command, argsJSON))
			},
		}

		turnCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		sess.Run(turnCtx, turnCfg, "say hi")
		sess.End("ok")

		var foundToolResult bool
		for _, ev := range events {
			if ev.Kind == chat.EventToolResult && strings.Contains(ev.ToolOutput, "hello, world") {
				foundToolResult = true
				break
			}
		}
		if !foundToolResult {
			var texts []string
			for _, ev := range events {
				texts = append(texts, ev.Kind.String()+"="+ev.ToolOutput)
			}
			t.Errorf("expected tool result with 'hello, world'; events: %v", texts)
		}
	})

	// --- Turn 2: on_tool_call blocks the dangerous bash call ---
	t.Run("on_tool_call_blocks_via_turn", func(t *testing.T) {
		fake := fakellm.New(
			fakellm.Script{Events: []llm.StreamEvent{
				{ToolCall: &llm.ToolCall{ID: "1", Name: "bash", RawArgs: `{"command":"rm -rf /"}`}},
			}},
			fakellm.Script{Events: []llm.StreamEvent{
				{TextDelta: "blocked"},
				{Usage: &llm.Usage{PromptTokens: 5, CompletionTokens: 1, TotalTokens: 6}},
			}},
		)

		a := lc.FirstAgent()
		customDefs := lc.CustomToolsFor(a.CustomTools)
		toolDefs := luacfg.ToolDefs(a.Gates, customDefs)

		var events []chat.Event
		sess := chat.NewSession(chat.SessionOpts{Sink: func(ev chat.Event) {
			events = append(events, ev)
		}})
		sess.Start(map[string]string{"mode": "test"})

		turnCfg := chat.TurnConfig{
			LLM:               fake,
			Personality:       persona.BasePersona("you are a test", toolDefs),
			StatusLine:        "test │ x",
			WorkDir:           dir,
			Log:               applog.Noop{},
			ResolveCustomTool: lc.ResolveCustomCall,
			CustomToolNames:   map[string]bool{"greet": true},
			RunToolCall: func(ctx context.Context, name, command, argsJSON string) chat.ToolCallVerdict {
				return bridgeVerdict(lc.RunToolCall(ctx, name, command, argsJSON))
			},
			Handlers: chat.NewHandlers(),
		}

		turnCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		sess.Run(turnCtx, turnCfg, "run dangerous command")
		sess.End("ok")

		// The bash handler returns "error: blocked by on_tool_call: ..." instead of
		// executing the command.
		var foundBlocked bool
		for _, ev := range events {
			if ev.Kind == chat.EventToolResult && strings.Contains(ev.ToolOutput, "blocked by on_tool_call") {
				foundBlocked = true
				break
			}
		}
		if !foundBlocked {
			var texts []string
			for _, ev := range events {
				texts = append(texts, ev.Kind.String()+"="+ev.ToolOutput)
			}
			t.Errorf("expected tool result blocked by on_tool_call; events: %v", texts)
		}
	})

	// --- Turn 3: on_tool_result redacts the model-visible bash output ---
	// Exercises the real dispatch path (turn.go applies cfg.RunToolResult to the
	// result before it reaches the model), not just the chain executor in isolation.
	t.Run("on_tool_result_redacts_via_turn", func(t *testing.T) {
		fake := fakellm.New(
			fakellm.Script{Events: []llm.StreamEvent{
				{ToolCall: &llm.ToolCall{ID: "1", Name: "bash", RawArgs: `{"command":"echo SECRET-TOKEN"}`}},
			}},
			fakellm.Script{Events: []llm.StreamEvent{
				{TextDelta: "done"},
				{Usage: &llm.Usage{PromptTokens: 5, CompletionTokens: 1, TotalTokens: 6}},
			}},
		)

		a := lc.FirstAgent()
		customDefs := lc.CustomToolsFor(a.CustomTools)
		toolDefs := luacfg.ToolDefs(a.Gates, customDefs)

		var events []chat.Event
		sess := chat.NewSession(chat.SessionOpts{Sink: func(ev chat.Event) {
			events = append(events, ev)
		}})
		sess.Start(map[string]string{"mode": "test"})

		turnCfg := chat.TurnConfig{
			LLM:               fake,
			Personality:       persona.BasePersona("you are a test", toolDefs),
			StatusLine:        "test │ x",
			WorkDir:           dir,
			Log:               applog.Noop{},
			ResolveCustomTool: lc.ResolveCustomCall,
			CustomToolNames:   map[string]bool{"greet": true},
			RunToolCall: func(ctx context.Context, name, command, argsJSON string) chat.ToolCallVerdict {
				return bridgeVerdict(lc.RunToolCall(ctx, name, command, argsJSON))
			},
			RunToolResult: func(ctx context.Context, name, argsJSON, output string) string {
				return lc.RunToolResult(ctx, name, argsJSON, output)
			},
			Handlers: chat.NewHandlers(),
		}

		turnCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		sess.Run(turnCtx, turnCfg, "echo a secret")
		sess.End("ok")

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
			t.Errorf("on_tool_result did not redact the model-visible output: %q", resultOut)
		}
		if !strings.Contains(resultOut, "[redacted]") {
			t.Errorf("expected redacted marker in tool output, got: %q", resultOut)
		}
	})

	// --- Turn 4: shell_interactive is gated by on_tool_call (no ungated bash path) ---
	// A dangerous command issued via shell_interactive must be blocked by the same
	// denylist as bash, and must never reach the PTY runner.
	t.Run("shell_interactive_gated_via_turn", func(t *testing.T) {
		fake := fakellm.New(
			fakellm.Script{Events: []llm.StreamEvent{
				{ToolCall: &llm.ToolCall{ID: "1", Name: "shell_interactive", RawArgs: `{"command":"rm -rf /"}`}},
			}},
			fakellm.Script{Events: []llm.StreamEvent{
				{TextDelta: "blocked"},
				{Usage: &llm.Usage{PromptTokens: 5, CompletionTokens: 1, TotalTokens: 6}},
			}},
		)

		a := lc.FirstAgent()
		customDefs := lc.CustomToolsFor(a.CustomTools)
		toolDefs := luacfg.ToolDefs(a.Gates, customDefs)

		var events []chat.Event
		sess := chat.NewSession(chat.SessionOpts{Sink: func(ev chat.Event) {
			events = append(events, ev)
		}})
		sess.Start(map[string]string{"mode": "test"})

		var ptyRan bool
		turnCfg := chat.TurnConfig{
			LLM:               fake,
			Personality:       persona.BasePersona("you are a test", toolDefs),
			StatusLine:        "test │ x",
			WorkDir:           dir,
			Log:               applog.Noop{},
			ResolveCustomTool: lc.ResolveCustomCall,
			CustomToolNames:   map[string]bool{"greet": true},
			RunToolCall: func(ctx context.Context, name, command, argsJSON string) chat.ToolCallVerdict {
				return bridgeVerdict(lc.RunToolCall(ctx, name, command, argsJSON))
			},
			ShellInteractive: func(_ context.Context, cmd, _ string) string {
				ptyRan = true
				return "ran: " + cmd
			},
			Handlers: chat.NewHandlers(),
		}

		turnCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		sess.Run(turnCtx, turnCfg, "open an interactive shell and wipe the disk")
		sess.End("ok")

		if ptyRan {
			t.Fatal("shell_interactive ran a command the denylist must block — ungated bash path")
		}
		var blocked bool
		for _, ev := range events {
			if ev.Kind == chat.EventToolResult && strings.Contains(ev.ToolOutput, "blocked by on_tool_call") {
				blocked = true
			}
		}
		if !blocked {
			var texts []string
			for _, ev := range events {
				texts = append(texts, ev.Kind.String()+"="+ev.ToolOutput)
			}
			t.Errorf("expected shell_interactive blocked by on_tool_call; events: %v", texts)
		}
	})

	// --- Turn 5: on_tool_call gates a NON-bash tool (read) via the dispatch loop ---
	// Proves the chain fires for every tool with its real name (here a read of
	// .env, blocked by name+args), not just the bash family.
	t.Run("non_bash_read_gated_via_turn", func(t *testing.T) {
		fake := fakellm.New(
			fakellm.Script{Events: []llm.StreamEvent{
				{ToolCall: &llm.ToolCall{ID: "1", Name: "read", RawArgs: `{"path":".env"}`}},
			}},
			fakellm.Script{Events: []llm.StreamEvent{
				{TextDelta: "blocked"},
				{Usage: &llm.Usage{PromptTokens: 5, CompletionTokens: 1, TotalTokens: 6}},
			}},
		)

		a := lc.FirstAgent()
		customDefs := lc.CustomToolsFor(a.CustomTools)
		toolDefs := luacfg.ToolDefs(a.Gates, customDefs)

		var events []chat.Event
		sess := chat.NewSession(chat.SessionOpts{Sink: func(ev chat.Event) {
			events = append(events, ev)
		}})
		sess.Start(map[string]string{"mode": "test"})

		turnCfg := chat.TurnConfig{
			LLM:               fake,
			Personality:       persona.BasePersona("you are a test", toolDefs),
			StatusLine:        "test │ x",
			WorkDir:           dir,
			Log:               applog.Noop{},
			ResolveCustomTool: lc.ResolveCustomCall,
			CustomToolNames:   map[string]bool{"greet": true},
			RunToolCall: func(ctx context.Context, name, command, argsJSON string) chat.ToolCallVerdict {
				return bridgeVerdict(lc.RunToolCall(ctx, name, command, argsJSON))
			},
			Handlers: chat.NewHandlers(),
		}

		turnCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		sess.Run(turnCtx, turnCfg, "read the env file")
		sess.End("ok")

		var blocked bool
		for _, ev := range events {
			if ev.Kind == chat.EventToolResult && strings.Contains(ev.ToolOutput, "no reading .env") {
				blocked = true
			}
		}
		if !blocked {
			var texts []string
			for _, ev := range events {
				texts = append(texts, ev.Kind.String()+"="+ev.ToolOutput)
			}
			t.Errorf("expected read of .env blocked by on_tool_call; events: %v", texts)
		}
	})
}

// TestLuacfgIntegration_EmptyRewriteOnNonBashFailsClosed drives the exact footgun
// the Passthrough signal exists to catch: a bash-oriented rewrite handler written
// WITHOUT the required t.name guard, using the "defensive" (t.command or "") form.
// For a non-bash tool t.command is nil, so it returns {command=""} — whose argv
// (["bash","-c",""]) is byte-identical to a pure pass. It must fail closed (the
// non-bash tool is blocked), not run, honoring the "command/argv verdicts are
// bash-only" invariant end-to-end through the real dispatch loop.
func TestLuacfgIntegration_EmptyRewriteOnNonBashFailsClosed(t *testing.T) {
	dir := t.TempDir()
	writeTmpFile(t, dir, "shell3.lua", `
shell3.model("m", { base_url = "https://api.example.com/v1", api_key = "sk-test", model = "x" })
shell3.on_tool_call(function(t)
  -- No t.name guard on purpose: a rewrite handler that assumes bash. For a
  -- non-bash tool this yields {command=""}, which must fail closed.
  return { command = (t.command or ""):gsub("rm", "echo") }
end)
shell3.agent({ name = "a", model = "m", prompt = "p", tools = { bash = true } })
`)

	lc, err := luacfg.Load(dir+"/shell3.lua", dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer lc.Close()

	fake := fakellm.New(
		fakellm.Script{Events: []llm.StreamEvent{
			{ToolCall: &llm.ToolCall{ID: "1", Name: "read", RawArgs: `{"path":"README.md"}`}},
		}},
		fakellm.Script{Events: []llm.StreamEvent{
			{TextDelta: "ok"},
			{Usage: &llm.Usage{PromptTokens: 5, CompletionTokens: 1, TotalTokens: 6}},
		}},
	)

	a := lc.FirstAgent()
	toolDefs := luacfg.ToolDefs(a.Gates, lc.CustomToolsFor(a.CustomTools))

	var events []chat.Event
	sess := chat.NewSession(chat.SessionOpts{Sink: func(ev chat.Event) { events = append(events, ev) }})
	sess.Start(map[string]string{"mode": "test"})

	turnCfg := chat.TurnConfig{
		LLM:         fake,
		Personality: persona.BasePersona("you are a test", toolDefs),
		StatusLine:  "test │ x",
		WorkDir:     dir,
		Log:         applog.Noop{},
		RunToolCall: func(ctx context.Context, name, command, argsJSON string) chat.ToolCallVerdict {
			return bridgeVerdict(lc.RunToolCall(ctx, name, command, argsJSON))
		},
		Handlers: chat.NewHandlers(),
	}

	turnCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	sess.Run(turnCtx, turnCfg, "read the readme")
	sess.End("ok")

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
