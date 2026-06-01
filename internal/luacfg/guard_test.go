package luacfg

import "testing"

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
	d, reason, err := c.OnToolCall(t.Context(), "edit_file", map[string]any{"file_path": "x"})
	if err != nil {
		t.Fatal(err)
	}
	if d != DecisionBlock || reason != "no edits" {
		t.Fatalf("guard: d=%v reason=%q", d, reason)
	}
	d2, _, _ := c.OnToolCall(t.Context(), "bash", map[string]any{"command": "ls"})
	if d2 != DecisionAllow {
		t.Fatalf("guard should allow bash, got %v", d2)
	}
}
