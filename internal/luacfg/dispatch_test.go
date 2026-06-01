package luacfg

import "testing"

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
