package luacfg

import "testing"

func TestCallToolNonStringReturn(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `
shell3.model("m", { base_url="u", api_key="k", model="x" })
local bad = shell3.tool({ name="bad", description="d",
  parameters={ type="object", properties={} },
  handler=function(args) return 42 end })
shell3.agent({ name="a", model="m", prompt="p", tools={ custom={ bad } } })
`)
	c, err := Load(dir+"/shell3.lua", dir)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_, err = c.CallTool(t.Context(), "bad", "{}")
	if err == nil {
		t.Fatal("expected error for non-string handler return, got nil")
	}
	if !contains(err.Error(), "must return a string") {
		t.Fatalf("expected 'must return a string' in error, got: %v", err)
	}
}

func TestCallTool(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `
shell3.model("m", { base_url="u", api_key="k", model="x" })
local echo = shell3.tool({ name="echo", description="d",
  parameters={ type="object", properties={ msg={ type="string" } }, required={"msg"} },
  handler=function(args) return "got:"..args.msg end })
shell3.agent({ name="a", model="m", prompt="p", tools={ custom={ echo } } })
`)
	c, err := Load(dir+"/shell3.lua", dir)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	out, err := c.CallTool(t.Context(), "echo", `{"msg":"hi"}`)
	if err != nil {
		t.Fatal(err)
	}
	if out != "got:hi" {
		t.Fatalf("CallTool: %q", out)
	}
}

func TestParseAction(t *testing.T) {
	cases := []struct {
		in   string
		want Decision
	}{
		{"", DecisionAllow},        // absent action: no opinion -> allow
		{"allow", DecisionAllow},   // explicit allow
		{"block", DecisionBlock},   // explicit block
		{"cancel", DecisionCancel}, // abort the turn
		{"ask", DecisionAsk},       // suspend for approval
		{"blok", DecisionBlock},    // typo -> fail closed
		{"BLOCK", DecisionBlock},   // wrong case is unknown -> fail closed
		{"deny", DecisionBlock},    // unknown -> fail closed
	}
	for _, tc := range cases {
		if got := parseAction(tc.in); got != tc.want {
			t.Errorf("parseAction(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// TestGuard_UnknownActionFailsClosed verifies that a guard returning a typo'd
// action string blocks the call rather than silently allowing it.
func TestGuard_UnknownActionFailsClosed(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `
shell3.model("m", { base_url="u", api_key="k", model="x" })
shell3.agent({ name="a", model="m", prompt="p", tools={ bash=true },
  on_tool_call = { function(call) return { action="blok", reason="typo" } end } })
`)
	c, err := Load(dir+"/shell3.lua", dir)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	d, _, err := c.OnToolCallFor(c.FirstAgent(), t.Context(), "bash", map[string]any{"command": "ls"})
	if err != nil {
		t.Fatal(err)
	}
	if d != DecisionBlock {
		t.Fatalf("unknown action should fail closed (block), got %v", d)
	}
}

func TestLuaBashViaCallTool(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `
shell3.model("m", { base_url="u", api_key="k", model="x" })
local hi = shell3.tool({ name="hi", description="d",
  parameters={ type="object", properties={} },
  handler=function(args) local r = shell3.bash("echo hello", { timeout=5 }); return r.stdout end })
shell3.agent({ name="a", model="m", prompt="p", tools={ custom={ hi } } })
`)
	c, err := Load(dir+"/shell3.lua", dir)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	out, err := c.CallTool(t.Context(), "hi", "{}")
	if err != nil {
		t.Fatal(err)
	}
	if out != "hello\n" {
		t.Fatalf("bash via CallTool: %q", out)
	}
}
