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
	c, err := Load(dir + "/shell3.lua")
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

	// Subagents() plural accessor must return exactly one entry
	if got := len(c.Subagents()); got != 1 {
		t.Fatalf("len(Subagents()) = %d, want 1", got)
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
	_, err := Load(dir + "/shell3.lua")
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
	_, err := Load(dir + "/shell3.lua")
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
	_, err := Load(dir + "/shell3.lua")
	if err == nil {
		t.Fatal("expected error for unknown subagent reference")
	}
	if !contains(err.Error(), "ghost") {
		t.Fatalf("error should mention 'ghost'; got: %v", err)
	}
}

func TestSubagent_NameCollisionWithAgentAutoSuffixes(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", minModelHdr+`
shell3.agent({ name = "dup", model = "m", prompt = "a" })
shell3.subagent({ name = "dup", description = "a duplicate", model = "m", prompt = "p" })
`)
	c, err := Load(dir + "/shell3.lua")
	if err != nil {
		t.Fatalf("agent/subagent name collision should auto-suffix, not error: %v", err)
	}
	defer c.Close()
	if _, ok := c.AgentByName("dup"); !ok {
		t.Fatal(`agent "dup" should keep its name`)
	}
	if _, ok := c.SubagentByName("dup2"); !ok {
		t.Fatalf(`colliding subagent should become "dup2"; got %+v`, c.Subagents())
	}
}

func TestAgent_SubagentsNonTableErrors(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", minModelHdr+`
shell3.agent({
	name = "code",
	model = "m",
	prompt = "c",
	tools = { bash = true, subagents = true },
})
`)
	_, err := Load(dir + "/shell3.lua")
	if err == nil {
		t.Fatal("expected error for non-table subagents on an agent")
	}
	if !contains(err.Error(), "tools.subagents must be a list") {
		t.Fatalf("error should mention 'tools.subagents must be a list'; got: %v", err)
	}
}

func TestAgent_SubagentsBareStringElementErrors(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", minModelHdr+`
shell3.subagent({ name = "explorer", description = "explores", model = "m", prompt = "p" })
shell3.agent({
	name = "code",
	model = "m",
	prompt = "c",
	tools = { bash = true, subagents = { "explorer" } },
})
`)
	_, err := Load(dir + "/shell3.lua")
	if err == nil {
		t.Fatal("expected error for bare-string subagents array element")
	}
	if !contains(err.Error(), "is not a subagent handle") {
		t.Fatalf("error should mention 'is not a subagent handle'; got: %v", err)
	}
}

func TestAgent_SubagentsEmptyTableOk(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", minModelHdr+`
shell3.agent({
	name = "code",
	model = "m",
	prompt = "c",
	tools = { bash = true, subagents = {} },
})
`)
	c, err := Load(dir + "/shell3.lua")
	if err != nil {
		t.Fatalf("expected no error for empty subagents table; got: %v", err)
	}
	defer c.Close()
	a, ok := c.AgentByName("code")
	if !ok {
		t.Fatal("AgentByName(code) returned ok=false")
	}
	if len(a.Subagents) != 0 {
		t.Fatalf("code.Subagents = %v, want empty", a.Subagents)
	}
}

func TestSubagent_FalseSubagentsKeyIsNotDepthError(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", minModelHdr+`
shell3.subagent({
	name = "worker",
	description = "does work",
	model = "m",
	prompt = "p",
	tools = { bash = true, subagents = false },
})
shell3.agent({ name = "code", model = "m", prompt = "c" })
`)
	c, err := Load(dir + "/shell3.lua")
	if err != nil {
		t.Fatalf("expected no error for subagents=false in subagent tools; got: %v", err)
	}
	defer c.Close()
	if _, ok := c.SubagentByName("worker"); !ok {
		t.Fatal("SubagentByName(worker) returned ok=false; subagent was not registered")
	}
}
