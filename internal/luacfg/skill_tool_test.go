package luacfg

import (
	"strings"
	"testing"
)

func TestSkillTool(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `
local s = shell3.skill({ name="web-search", description="d", body="THE BODY" })
shell3.model("m", { base_url="u", api_key="k", model="x" })
shell3.agent({ name="a", model="m", prompt="p", tools={}, skills={ s } })
`)
	c, err := Load(dir+"/shell3.lua", dir)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	// CallTool("skill", ...) must return the body for a known skill.
	got, err := c.CallTool(t.Context(), "skill", `{"name":"web-search"}`)
	if err != nil {
		t.Fatalf("CallTool skill: unexpected error: %v", err)
	}
	if got != "THE BODY" {
		t.Fatalf("expected %q, got %q", "THE BODY", got)
	}

	// Unknown skill name must return an error.
	_, err = c.CallTool(t.Context(), "skill", `{"name":"no-such-skill"}`)
	if err == nil {
		t.Fatal("expected error for unknown skill name, got nil")
	}
	if !strings.Contains(err.Error(), "no-such-skill") {
		t.Fatalf("error should mention the bad name, got: %v", err)
	}
}

func TestToolDefsIncludesSkill(t *testing.T) {
	withSkill := ToolDefs(ToolGates{}, nil, true)
	found := false
	for _, d := range withSkill {
		if d.Name == "skill" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("ToolDefs(hasSkills=true) did not include a 'skill' tool definition")
	}

	withoutSkill := ToolDefs(ToolGates{}, nil, false)
	for _, d := range withoutSkill {
		if d.Name == "skill" {
			t.Fatal("ToolDefs(hasSkills=false) should NOT include a 'skill' tool definition")
		}
	}
}
