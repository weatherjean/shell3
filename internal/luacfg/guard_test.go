package luacfg

import (
	"context"
	"testing"
)

func TestGuardChainBlocks(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `
shell3.model("m", { base_url="u", api_key="k", model="x" })
local g = {
  function(call)
    if call.tool == "edit_file" then return { action="block", reason="no edits" } end
    return { action="allow" }
  end,
}
shell3.agent({ name="a", model="m", prompt="p", tools={ edit=true }, on_tool_call=g })
`)
	c, err := Load(dir+"/shell3.lua", dir)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	a := c.FirstAgent()
	d, reason, err := c.OnToolCallFor(a, t.Context(), "edit_file", map[string]any{"file_path": "x"})
	if err != nil {
		t.Fatal(err)
	}
	if d != DecisionBlock || reason != "no edits" {
		t.Fatalf("guard: d=%v reason=%q", d, reason)
	}
	d2, _, _ := c.OnToolCallFor(a, t.Context(), "bash", map[string]any{"command": "ls"})
	if d2 != DecisionAllow {
		t.Fatalf("guard should allow bash, got %v", d2)
	}
}

// TestGuard_AskDecision: a guard returning action="ask" yields DecisionAsk
// with the reason passed through.
func TestGuard_AskDecision(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `
shell3.model("m", { base_url = "http://x", api_key = "k", model = "mm" })
shell3.agent({ name = "a", model = "m", prompt = "p",
  on_tool_call = { function(call) return { action = "ask", reason = "needs a human" } end } })
`)
	c, err := Load(dir+"/shell3.lua", dir)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	a := c.FirstAgent()
	d, reason, err := c.OnToolCallFor(a, context.Background(), "bash", map[string]any{"command": "rm -rf /"})
	if err != nil {
		t.Fatal(err)
	}
	if d != DecisionAsk || reason != "needs a human" {
		t.Fatalf("got (%v, %q), want (DecisionAsk, \"needs a human\")", d, reason)
	}
}

func TestOnToolCall_LuaError_FailsClosed(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `
shell3.model("m", { base_url="u", api_key="k", model="x" })
local g = {
  function(call) error("boom") end,
}
shell3.agent({ name="a", model="m", prompt="p", tools={ bash=true }, on_tool_call=g })
`)
	c, err := Load(dir+"/shell3.lua", dir)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	d, reason, err := c.OnToolCallFor(c.FirstAgent(), t.Context(), "bash", map[string]any{"command": "echo hi"})
	if err != nil {
		t.Fatalf("OnToolCall returned hard error: %v", err)
	}
	if d != DecisionBlock {
		t.Fatalf("guard error should fail closed (block), got %v (reason=%q)", d, reason)
	}
}
