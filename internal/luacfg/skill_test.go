package luacfg

import "testing"

func TestLoadSkill(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `
local s = shell3.skill({ name="web-search", description="d", body="B" })
shell3.model("m", { base_url="u", api_key="k", model="x" })
shell3.agent({ name="a", model="m", prompt="p", tools={}, skills={ s } })
`)
	c, err := Load(dir+"/shell3.lua", dir)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if len(c.Skills) != 1 || c.Skills[0].Name != "web-search" || c.Skills[0].Body != "B" {
		t.Fatalf("bad skills: %+v", c.Skills)
	}
	if len(c.FirstAgent().Skills) != 1 || c.FirstAgent().Skills[0] != "web-search" {
		t.Fatalf("agent skills not linked: %+v", c.FirstAgent().Skills)
	}
}
