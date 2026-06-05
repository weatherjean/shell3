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

// TestLuacfgIntegration_GuardAndCustomTool loads a luacfg config and drives a
// full chat turn through the pkg/chat turn loop using fakellm. It asserts:
//   - A custom tool call returns the handler's string output.
//   - A bash call with a dangerous command is blocked by a custom guard.
func TestLuacfgIntegration_GuardAndCustomTool(t *testing.T) {
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
  handler = function(args)
    return "hello, " .. (args.name or "world")
  end,
})

local function guard_block_rm(call)
  local cmd = tostring((call.params or {}).command or "")
  if cmd:match("rm") then
    return { action = "block", reason = "blocked dangerous command" }
  end
  return { action = "allow" }
end

shell3.agent({
  name  = "test-agent",
  model = "m",
  prompt = "you are a test agent",
  tools = {
    bash   = true,
    custom = { greet },
  },
  on_tool_call = { guard_block_rm },
})
`)

	lc, err := luacfg.Load(dir+"/shell3.lua", dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer lc.Close()

	// Verify wiring closures directly before running the full turn.
	ctx := context.Background()

	// Custom tool closure: greet tool should return "hello, test"
	toolOut, err := lc.CallTool(ctx, "greet", `{"name":"test"}`)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if toolOut != "hello, test" {
		t.Errorf("CallTool output: got %q, want %q", toolOut, "hello, test")
	}

	// Guard closure: rm -rf / should be blocked
	d, reason, err := lc.OnToolCall(ctx, "bash", map[string]any{"command": "rm -rf /"})
	if err != nil {
		t.Fatalf("OnToolCall: %v", err)
	}
	if d != luacfg.DecisionBlock {
		t.Errorf("guard: expected DecisionBlock for rm -rf /, got %v (reason=%q)", d, reason)
	}
	if !strings.Contains(reason, "dangerous") {
		t.Errorf("guard reason should mention 'dangerous', got: %q", reason)
	}

	// Guard closure: safe bash command should be allowed
	d2, _, err := lc.OnToolCall(ctx, "bash", map[string]any{"command": "echo hello"})
	if err != nil {
		t.Fatalf("OnToolCall (safe): %v", err)
	}
	if d2 != luacfg.DecisionAllow {
		t.Errorf("guard: expected DecisionAllow for echo hello, got %v", d2)
	}

	// Now run the fuller turn-loop test using fakellm.
	// Script:
	//   Turn 1: assistant calls greet("world"), then returns final text "done".
	//   Turn 2 (scripted as the follow-up after tool result): assistant calls
	//     bash("rm -rf /"), which is blocked, then returns "done".
	// We use two separate sessions to keep the assertions clean.

	// --- Turn 1: custom tool call path ---
	t.Run("custom_tool_via_turn", func(t *testing.T) {
		fake := fakellm.New(
			// First stream call: emit a greet tool call.
			fakellm.Script{Events: []llm.StreamEvent{
				{ToolCall: &llm.ToolCall{
					ID:      "1",
					Name:    "greet",
					RawArgs: `{"name":"world"}`,
				}},
			}},
			// Second stream call (after tool result): emit final text.
			fakellm.Script{Events: []llm.StreamEvent{
				{TextDelta: "done"},
				{Usage: &llm.Usage{PromptTokens: 5, CompletionTokens: 1, TotalTokens: 6}},
			}},
		)

		customNames := map[string]bool{"greet": true}
		toolGuard := func(ctx context.Context, t string, p map[string]any) (int, string, error) {
			d, r, e := lc.OnToolCall(ctx, t, p)
			return int(d), r, e
		}
		customDefs := lc.CustomToolsFor(lc.Active().CustomTools)
		toolDefs := luacfg.ToolDefs(lc.Active().Gates, customDefs, len(lc.Active().Skills) > 0)

		var events []chat.Event
		sess := chat.NewSession(chat.SessionOpts{Sink: func(ev chat.Event) {
			events = append(events, ev)
		}})
		sess.Start(map[string]string{"mode": "test"})

		turnCfg := chat.TurnConfig{
			LLM:             fake,
			Personality:     persona.BasePersona("you are a test", toolDefs),
			StatusLine:      "test │ x",
			Log:             applog.Noop{},
			CustomTool:      lc.CallTool,
			CustomToolNames: customNames,
			ToolGuard:       toolGuard,
		}

		turnCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		sess.Run(turnCtx, turnCfg, "say hi")
		sess.End("ok")

		// Check that a tool_result event containing "hello, world" was emitted.
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

	// --- Turn 2: guard blocks dangerous bash call ---
	t.Run("guard_blocks_bash_via_turn", func(t *testing.T) {
		fake := fakellm.New(
			// First stream call: emit a bash tool call with dangerous command.
			fakellm.Script{Events: []llm.StreamEvent{
				{ToolCall: &llm.ToolCall{
					ID:      "1",
					Name:    "bash",
					RawArgs: `{"command":"rm -rf /"}`,
				}},
			}},
			// Second stream call (after tool result with block message): final text.
			fakellm.Script{Events: []llm.StreamEvent{
				{TextDelta: "blocked"},
				{Usage: &llm.Usage{PromptTokens: 5, CompletionTokens: 1, TotalTokens: 6}},
			}},
		)

		customNames := map[string]bool{"greet": true}
		toolGuard := func(ctx context.Context, t string, p map[string]any) (int, string, error) {
			d, r, e := lc.OnToolCall(ctx, t, p)
			return int(d), r, e
		}
		customDefs := lc.CustomToolsFor(lc.Active().CustomTools)
		toolDefs := luacfg.ToolDefs(lc.Active().Gates, customDefs, len(lc.Active().Skills) > 0)

		var events []chat.Event
		sess := chat.NewSession(chat.SessionOpts{Sink: func(ev chat.Event) {
			events = append(events, ev)
		}})
		sess.Start(map[string]string{"mode": "test"})

		turnCfg := chat.TurnConfig{
			LLM:             fake,
			Personality:     persona.BasePersona("you are a test", toolDefs),
			StatusLine:      "test │ x",
			Log:             applog.Noop{},
			CustomTool:      lc.CallTool,
			CustomToolNames: customNames,
			ToolGuard:       toolGuard,
		}

		turnCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		sess.Run(turnCtx, turnCfg, "run dangerous command")
		sess.End("ok")

		// Check that a tool_result event containing "USER DENIED" was emitted
		// (turn.go emits this when the guard returns DecisionBlock).
		var foundDenied bool
		for _, ev := range events {
			if ev.Kind == chat.EventToolResult && strings.Contains(ev.ToolOutput, "USER DENIED") {
				foundDenied = true
				break
			}
		}
		if !foundDenied {
			var texts []string
			for _, ev := range events {
				texts = append(texts, ev.Kind.String()+"="+ev.ToolOutput)
			}
			t.Errorf("expected tool result with 'USER DENIED'; events: %v", texts)
		}
	})
}
