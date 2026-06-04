package luacfg

import "testing"

func TestLoadTool(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `
local echo = shell3.tool({
  name="echo", description="d",
  parameters={ type="object", properties={ msg={ type="string" } }, required={"msg"} },
  handler=function(args) return "got:"..args.msg end,
})
shell3.model("m", { base_url="u", api_key="k", model="x" })
shell3.agent({ name="a", model="m", prompt="p", tools={ custom={ echo } } })
`)
	c, err := Load(dir+"/shell3.lua", dir)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, ok := c.Tools["echo"]; !ok {
		t.Fatalf("tool not registered: %+v", c.Tools)
	}
	if len(c.Active().CustomTools) != 1 || c.Active().CustomTools[0] != "echo" {
		t.Fatalf("agent custom tools not linked: %+v", c.Active().CustomTools)
	}
}
