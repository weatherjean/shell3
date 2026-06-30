package luacfg

import "testing"

// loadLua writes a shell3.lua with a "main" model prelude plus body and loads it.
func loadLua(t *testing.T, body string) *LoadedConfig {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `
shell3.model("main", { base_url="u", api_key="k", model="x" })
`+body)
	c, err := Load(dir+"/shell3.lua", dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// Duplicate agent names auto-suffix instead of failing the load: the first
// keeps its bare name, later collisions become name2, name3…
func TestDuplicateAgentNameAutoSuffixes(t *testing.T) {
	c := loadLua(t, `
shell3.agent({ name="code", model="main", prompt="a", tools={} })
shell3.agent({ name="code", model="main", prompt="b", tools={} })
shell3.agent({ name="code", model="main", prompt="c", tools={} })
`)
	got := c.AgentNames()
	want := []string{"code", "code2", "code3"}
	if len(got) != len(want) {
		t.Fatalf("agent names = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("agent names = %v, want %v", got, want)
		}
	}
}

// AgentNames is a thin helper for the test above.
func (c *LoadedConfig) AgentNames() []string {
	out := make([]string, 0, len(c.agents))
	for _, a := range c.Agents() {
		out = append(out, a.Name)
	}
	return out
}

// Agents and subagents share one namespace, and the handle returned by
// shell3.subagent carries the deduped name, so a colliding subagent is renamed
// and tools.subagents={handle} still resolves to the renamed entry.
func TestAgentSubagentNamespaceShared(t *testing.T) {
	c := loadLua(t, `
shell3.agent({ name="dev", model="main", prompt="a", tools={} })
local helper = shell3.subagent({ name="dev", description="d", model="main", prompt="p", tools={} })
shell3.agent({ name="lead", model="main", prompt="b", tools={ subagents={helper} } })
`)
	if _, ok := c.AgentByName("dev"); !ok {
		t.Fatal(`first agent "dev" should keep its name`)
	}
	sa, ok := c.SubagentByName("dev2")
	if !ok {
		t.Fatalf(`subagent should dedup to "dev2"; subagents=%+v`, c.Subagents())
	}
	if _, ok := c.SubagentByName("dev"); ok {
		t.Fatal(`subagent "dev" should have been renamed away from the agent's name`)
	}
	lead, ok := c.AgentByName("lead")
	if !ok || len(lead.Subagents) != 1 || lead.Subagents[0] != sa.Name {
		t.Fatalf(`lead.Subagents should reference %q, got %+v`, sa.Name, lead.Subagents)
	}
}
