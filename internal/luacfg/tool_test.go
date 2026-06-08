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

func TestToolDefs_MediaGate(t *testing.T) {
	defs := ToolDefs(ToolGates{Media: true}, nil, false)
	var found bool
	for _, d := range defs {
		if d.Name == "read_media" {
			found = true
			props, ok := d.Parameters["properties"].(map[string]any)
			if !ok {
				t.Fatalf("read_media schema has no properties map")
			}
			if _, ok := props["path"]; !ok {
				t.Errorf("read_media schema missing 'path' property")
			}
		}
	}
	if !found {
		t.Fatalf("read_media not present when Media gate on; got %d defs", len(defs))
	}

	off := ToolDefs(ToolGates{}, nil, false)
	for _, d := range off {
		if d.Name == "read_media" {
			t.Fatalf("read_media present with Media gate off")
		}
	}
}
