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

// TestLuacfgIntegration_WrapBashAndCustomTool loads a luacfg config and drives a
// full chat turn through the chat turn loop using fakellm. It asserts:
//   - A custom (bash command-template) tool call runs its command with the
//     declared param exported into the env and returns the command's stdout.
//   - A bash call with a dangerous command is blocked by shell3.wrap_bash
//     (the guard engine's replacement) before it ever executes.
func TestLuacfgIntegration_WrapBashAndCustomTool(t *testing.T) {
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

-- Block any rm command; rewrite nothing else.
shell3.wrap_bash(function(cmd)
  if cmd:match("rm") then return nil, "blocked dangerous command" end
  return cmd
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

	// wrap_bash closure: rm -rf / should be blocked, echo allowed.
	if !lc.HasWrapBash() {
		t.Fatal("expected a wrap_bash hook to be declared")
	}
	_, allowed, reason, err := lc.WrapBash(ctx, "rm -rf /")
	if err != nil {
		t.Fatalf("WrapBash: %v", err)
	}
	if allowed {
		t.Error("wrap_bash should block rm -rf /")
	}
	if !strings.Contains(reason, "dangerous") {
		t.Errorf("wrap_bash reason should mention 'dangerous', got: %q", reason)
	}
	argv, allowed2, _, err := lc.WrapBash(ctx, "echo hello")
	if err != nil {
		t.Fatalf("WrapBash (safe): %v", err)
	}
	if !allowed2 || len(argv) != 3 || argv[0] != "bash" || argv[1] != "-c" || argv[2] != "echo hello" {
		t.Errorf("wrap_bash should allow 'echo hello' as bash -c argv, got allowed=%v argv=%q", allowed2, argv)
	}

	wrapBash := func(ctx context.Context, cmd string) ([]string, bool, string, error) {
		return lc.WrapBash(ctx, cmd)
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
			WrapBash:          wrapBash,
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

	// --- Turn 2: wrap_bash blocks the dangerous bash call ---
	t.Run("wrap_bash_blocks_via_turn", func(t *testing.T) {
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
			WrapBash:          wrapBash,
			Handlers:          chat.NewHandlers(chat.Config{}),
		}

		turnCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		sess.Run(turnCtx, turnCfg, "run dangerous command")
		sess.End("ok")

		// The bash handler returns "error: blocked by wrap_bash: ..." instead of
		// executing the command.
		var foundBlocked bool
		for _, ev := range events {
			if ev.Kind == chat.EventToolResult && strings.Contains(ev.ToolOutput, "blocked by wrap_bash") {
				foundBlocked = true
				break
			}
		}
		if !foundBlocked {
			var texts []string
			for _, ev := range events {
				texts = append(texts, ev.Kind.String()+"="+ev.ToolOutput)
			}
			t.Errorf("expected tool result blocked by wrap_bash; events: %v", texts)
		}
	})
}
