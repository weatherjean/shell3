package luacfg

import (
	"os"
	"path/filepath"
	"testing"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "shell3.lua")
	if err := os.WriteFile(p, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

const twoModelsHdr = `
shell3.model("opus",  { base_url="http://x", api_key="k", model="opus-id" })
shell3.model("haiku", { base_url="http://x", api_key="k", model="haiku-id" })
`

func TestMultipleAgentsAccumulateFirstActive(t *testing.T) {
	p := writeConfig(t, twoModelsHdr+`
shell3.agent({ name="build", model="opus",  prompt="b" })
shell3.agent({ name="plan",  model="haiku", prompt="p" })
`)
	c, err := Load(p, filepath.Dir(p))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if got := c.FirstAgent().Name; got != "build" {
		t.Fatalf("first agent = %q, want build (first declared)", got)
	}
	names := []string{}
	for _, a := range c.Agents() {
		names = append(names, a.Name)
	}
	if len(names) != 2 || names[0] != "build" || names[1] != "plan" {
		t.Fatalf("agent order = %v, want [build plan]", names)
	}
}

func TestDuplicateAgentNameErrors(t *testing.T) {
	p := writeConfig(t, twoModelsHdr+`
shell3.agent({ name="dup", model="opus", prompt="a" })
shell3.agent({ name="dup", model="opus", prompt="b" })
`)
	if _, err := Load(p, filepath.Dir(p)); err == nil {
		t.Fatal("duplicate agent name should error")
	}
}

func TestAgentModelDefaultsToFirstModel(t *testing.T) {
	p := writeConfig(t, twoModelsHdr+`
shell3.agent({ name="build", prompt="b" })
`)
	c, err := Load(p, filepath.Dir(p))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if got := c.FirstAgent().ModelName; got != "opus" {
		t.Fatalf("model fallback = %q, want opus (first declared)", got)
	}
}

func TestSingleAgentBackCompat(t *testing.T) {
	p := writeConfig(t, twoModelsHdr+`
shell3.agent({ name="base", model="opus", prompt="x" })
`)
	c, err := Load(p, filepath.Dir(p))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.FirstAgent().Name != "base" || len(c.Agents()) != 1 {
		t.Fatalf("single-agent back-compat broken: %q / %d", c.FirstAgent().Name, len(c.Agents()))
	}
}

// TestAgentByName_LookupAndMiss pins the name-parameterized agent lookup that
// replaces process-global active-agent state: sessions own their agent choice.
// Combines lookup, miss, FirstAgent-still-first, and BuildPersonaFor assertions.
func TestAgentByName_LookupAndMiss(t *testing.T) {
	p := writeConfig(t, `
shell3.model("m", { base_url = "http://x", api_key = "k", model = "mm" })
shell3.agent({ name = "code", model = "m", prompt = "c" })
shell3.agent({ name = "plan", model = "m", prompt = "p" })
`)
	c, err := Load(p, filepath.Dir(p))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	a, ok := c.AgentByName("plan")
	if !ok || a.Name != "plan" || a.Prompt != "p" {
		t.Fatalf("AgentByName(plan) = %+v, %t", a, ok)
	}
	// Unknown name reports ok=false; no global state is mutated.
	if _, ok := c.AgentByName("nope"); ok {
		t.Fatal("AgentByName(nope) should report ok=false")
	}
	// Lookup again after miss: first agent still accessible via FirstAgent.
	if c.FirstAgent().Name != "code" {
		t.Fatal("FirstAgent should still be code after a failed lookup")
	}
	// BuildPersonaFor renders the *given* agent, independent of any global.
	if got := c.BuildPersonaFor(a); got != "p" {
		t.Fatalf("BuildPersonaFor(plan) = %q, want %q", got, "p")
	}
}
