package luacfg

import (
	"strings"
	"testing"
)

// TestSkillsDisabledViaTools verifies that tools = { skill = false } suppresses
// both the "## Skills" prompt injection and the "skill" tool definition.
func TestSkillsDisabledViaTools(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `
local s = shell3.skill({ name="web-search", description="searches the web", body="B" })
shell3.model("m", { base_url="u", api_key="k", model="x" })
shell3.agent({ name="a", model="m", prompt="p", tools={ skill=false }, skills={ s } })
`)
	c, err := Load(dir+"/shell3.lua", dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer c.Close()

	// SkillsActive() must be false when skill=false is set.
	if c.Agent.SkillsActive() {
		t.Error("SkillsActive() = true, want false when tools.skill=false")
	}

	// BuildPersona must NOT inject the ## Skills section.
	persona := c.BuildPersona(RuntimeData{})
	if strings.Contains(persona, "## Skills") {
		t.Error("BuildPersona injected '## Skills' but skills are disabled")
	}
	if strings.Contains(persona, "web-search") {
		t.Error("BuildPersona injected skill name 'web-search' but skills are disabled")
	}

	// ToolDefs must NOT include the "skill" tool when SkillsActive() is false.
	defs := ToolDefs(c.Agent.Gates, nil, c.Agent.SkillsActive())
	for _, d := range defs {
		if d.Name == "skill" {
			t.Error("ToolDefs included 'skill' tool but skills are disabled")
		}
	}
}

// TestSkillsEnabledByDefault verifies that omitting skill=false in tools
// keeps the normal behavior: ## Skills injected, skill tool present.
func TestSkillsEnabledByDefault(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `
local s = shell3.skill({ name="web-search", description="searches the web", body="B" })
shell3.model("m", { base_url="u", api_key="k", model="x" })
shell3.agent({ name="a", model="m", prompt="p", tools={}, skills={ s } })
`)
	c, err := Load(dir+"/shell3.lua", dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer c.Close()

	// SkillsActive() must be true when skill key is absent.
	if !c.Agent.SkillsActive() {
		t.Error("SkillsActive() = false, want true when skill key is absent from tools")
	}

	// BuildPersona must inject the ## Skills section.
	persona := c.BuildPersona(RuntimeData{})
	if !strings.Contains(persona, "## Skills") {
		t.Error("BuildPersona did not inject '## Skills' but skills are enabled")
	}
	if !strings.Contains(persona, "web-search") {
		t.Error("BuildPersona did not inject skill name 'web-search'")
	}

	// ToolDefs must include the "skill" tool.
	defs := ToolDefs(c.Agent.Gates, nil, c.Agent.SkillsActive())
	found := false
	for _, d := range defs {
		if d.Name == "skill" {
			found = true
			break
		}
	}
	if !found {
		t.Error("ToolDefs did not include 'skill' tool but skills are enabled")
	}
}

// TestSkillsEnabledExplicitTrue verifies that tools = { skill = true } is
// treated the same as omitting the key: skills remain enabled.
func TestSkillsEnabledExplicitTrue(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `
local s = shell3.skill({ name="web-search", description="searches the web", body="B" })
shell3.model("m", { base_url="u", api_key="k", model="x" })
shell3.agent({ name="a", model="m", prompt="p", tools={ skill=true }, skills={ s } })
`)
	c, err := Load(dir+"/shell3.lua", dir)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	defer c.Close()

	if !c.Agent.SkillsActive() {
		t.Error("SkillsActive() = false, want true when tools.skill=true")
	}
}

// TestSkillKeyAllowed verifies that tools = { skill = false } does NOT trigger
// an unknown-key error (the key must be in toolGateKeys).
func TestSkillKeyAllowed(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `
local s = shell3.skill({ name="s", description="d", body="b" })
shell3.model("m", { base_url="u", api_key="k", model="x" })
shell3.agent({ name="a", model="m", prompt="p", tools={ skill=false }, skills={ s } })
`)
	_, err := Load(dir+"/shell3.lua", dir)
	if err != nil {
		t.Fatalf("tools={skill=false} should not cause an error, got: %v", err)
	}
}

// TestBogusToolKeyStillRejected verifies that a misspelled key like "skil"
// still triggers an unknown-key error.
func TestBogusToolKeyStillRejected(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `
shell3.model("m", { base_url="u", api_key="k", model="x" })
shell3.agent({ name="a", model="m", prompt="p", tools={ skil=false } })
`)
	_, err := Load(dir+"/shell3.lua", dir)
	if err == nil {
		t.Fatal("expected unknown-key error for tools={skil=false}, got nil")
	}
	if !strings.Contains(err.Error(), "skil") {
		t.Fatalf("error should mention the bad key 'skil', got: %v", err)
	}
}
