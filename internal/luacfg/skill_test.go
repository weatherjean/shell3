package luacfg

import "testing"

func TestLoadSkill(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "web.md", "B\n")
	writeFile(t, dir, "shell3.lua", `
local s = shell3.skill({ name="web-search", description="d", path="web.md" })
shell3.model("m", { base_url="u", api_key="k", model="x" })
shell3.agent({ name="a", model="m", prompt="p", tools={}, skills={ s } })
`)
	c, err := Load(dir + "/shell3.lua")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if len(c.Skills) != 1 || c.Skills[0].Name != "web-search" || c.Skills[0].Path != dir+"/web.md" {
		t.Fatalf("bad skills: %+v", c.Skills)
	}
	if len(c.FirstAgent().Skills) != 1 || c.FirstAgent().Skills[0] != "web-search" {
		t.Fatalf("agent skills not linked: %+v", c.FirstAgent().Skills)
	}
}

// A typo'd entry in skills={} (a bare string instead of a shell3.skill handle)
// must fail the load loudly — silently dropping it would yield an agent
// quietly missing the grant.
func TestSkillsListRejectsNonHandle(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `
shell3.model("m", { base_url = "u", api_key = "k", model = "x" })
shell3.agent({ name = "a", model = "m", prompt = "p", skills = { "web" } })
`)
	_, err := Load(dir + "/shell3.lua")
	if err == nil || !contains(err.Error(), "skill handle") {
		t.Fatalf("want a skill-handle load error, got %v", err)
	}
}
