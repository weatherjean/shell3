package luacfg

import (
	"testing"
)

// minModelHdr is a minimal model declaration header for subagent tests.
const minModelHdr = `shell3.model("m", { base_url="http://x", api_key="k", model="mid" })
`

func TestSubagent_RegistersSeparateFromAgents(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", minModelHdr+`
local researcher = shell3.subagent({
	name = "researcher",
	description = "investigate the repo",
	model = "m",
	prompt = "you research",
	tools = { bash = true },
})
shell3.agent({
	name = "code",
	model = "m",
	prompt = "c",
	tools = { bash = true, subagents = { researcher } },
})
`)
	c, err := Load(dir+"/shell3.lua", dir)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	// researcher must NOT appear in Agents()
	for _, a := range c.Agents() {
		if a.Name == "researcher" {
			t.Fatal("researcher should not appear in Agents()")
		}
	}

	// SubagentByName("researcher") must return ok with correct description
	sa, ok := c.SubagentByName("researcher")
	if !ok {
		t.Fatal("SubagentByName(researcher) returned ok=false")
	}
	if sa.Description != "investigate the repo" {
		t.Fatalf("Description = %q, want %q", sa.Description, "investigate the repo")
	}

	// code agent must list researcher in Subagents
	code, ok := c.AgentByName("code")
	if !ok {
		t.Fatal("AgentByName(code) returned ok=false")
	}
	if len(code.Subagents) != 1 || code.Subagents[0] != "researcher" {
		t.Fatalf("code.Subagents = %v, want [researcher]", code.Subagents)
	}
}

func TestSubagent_MissingDescriptionErrors(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", minModelHdr+`
shell3.subagent({ name = "noDesc", model = "m", prompt = "p" })
shell3.agent({ name = "code", model = "m", prompt = "c" })
`)
	_, err := Load(dir+"/shell3.lua", dir)
	if err == nil {
		t.Fatal("expected error for missing description")
	}
	if !contains(err.Error(), "description") {
		t.Fatalf("error should mention 'description'; got: %v", err)
	}
}

func TestSubagent_NestedSubagentsErrors(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", minModelHdr+`
local inner = shell3.subagent({
	name = "inner",
	description = "inner agent",
	model = "m",
	prompt = "p",
})
shell3.subagent({
	name = "outer",
	description = "outer agent",
	model = "m",
	prompt = "p",
	tools = { subagents = { inner } },
})
shell3.agent({ name = "code", model = "m", prompt = "c" })
`)
	_, err := Load(dir+"/shell3.lua", dir)
	if err == nil {
		t.Fatal("expected error for nested subagents")
	}
	if !contains(err.Error(), "subagent") {
		t.Fatalf("error should mention 'subagent'; got: %v", err)
	}
}

func TestSubagent_UnknownReferenceErrors(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", minModelHdr+`
local ghost = {}
ghost["__subagent"] = "ghost"
shell3.agent({
	name = "code",
	model = "m",
	prompt = "c",
	tools = { subagents = { ghost } },
})
`)
	_, err := Load(dir+"/shell3.lua", dir)
	if err == nil {
		t.Fatal("expected error for unknown subagent reference")
	}
	if !contains(err.Error(), "ghost") {
		t.Fatalf("error should mention 'ghost'; got: %v", err)
	}
}

func TestSubagent_NameCollisionWithAgentErrors(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", minModelHdr+`
shell3.agent({ name = "dup", model = "m", prompt = "a" })
shell3.subagent({ name = "dup", description = "a duplicate", model = "m", prompt = "p" })
`)
	_, err := Load(dir+"/shell3.lua", dir)
	if err == nil {
		t.Fatal("expected error for name collision between agent and subagent")
	}
	if !contains(err.Error(), "dup") {
		t.Fatalf("error should mention 'dup'; got: %v", err)
	}
}
