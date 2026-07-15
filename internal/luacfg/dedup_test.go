package luacfg

import "testing"

// loadLua writes a shell3.lua with a "main" model prelude plus body and loads it.
func loadLua(t *testing.T, body string) *LoadedConfig {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `
shell3.model("main", { base_url="u", api_key="k", model="x" })
`+body)
	c, err := Load(dir + "/shell3.lua")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// Duplicate subagent names auto-suffix instead of failing the load: the first
// keeps its bare name, later collisions become name2, name3…
func TestDuplicateSubagentNameAutoSuffixes(t *testing.T) {
	c := loadLua(t, `
shell3.subagent({ name="helper", description="d", model="main", prompt="a", tools={} })
shell3.subagent({ name="helper", description="d", model="main", prompt="b", tools={} })
shell3.subagent({ name="helper", description="d", model="main", prompt="c", tools={} })
shell3.agent({ name="code", model="main", prompt="p", tools={} })
`)
	got := []string{}
	for _, s := range c.Subagents() {
		got = append(got, s.Name)
	}
	want := []string{"helper", "helper2", "helper3"}
	if len(got) != len(want) {
		t.Fatalf("subagent names = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("subagent names = %v, want %v", got, want)
		}
	}
}

// Agents and subagents share one namespace: an agent whose name collides with
// an already-declared subagent is auto-suffixed away from it.
func TestAgentSubagentNamespaceShared(t *testing.T) {
	c := loadLua(t, `
local helper = shell3.subagent({ name="dev", description="d", model="main", prompt="p", tools={} })
shell3.agent({ name="dev", model="main", prompt="a", tools={ subagents={helper} } })
`)
	if _, ok := c.SubagentByName("dev"); !ok {
		t.Fatal(`subagent "dev" should keep its name (declared first)`)
	}
	a := c.FirstAgent()
	if a.Name != "dev2" {
		t.Fatalf(`agent should dedup to "dev2", got %q`, a.Name)
	}
	if len(a.Subagents) != 1 || a.Subagents[0] != "dev" {
		t.Fatalf(`agent.Subagents should reference "dev", got %+v`, a.Subagents)
	}
}
