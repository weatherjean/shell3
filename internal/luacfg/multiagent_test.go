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

func TestSecondAgentDeclarationErrors(t *testing.T) {
	p := writeConfig(t, twoModelsHdr+`
shell3.agent({ name="build", model="opus",  prompt="b" })
shell3.agent({ name="plan",  model="haiku", prompt="p" })
`)
	_, err := Load(p)
	if err == nil || !contains(err.Error(), "only one shell3.agent") {
		t.Fatalf("second shell3.agent should fail the load, got err=%v", err)
	}
}

func TestSingleAgentWithSubagentsLoads(t *testing.T) {
	p := writeConfig(t, twoModelsHdr+`
local a = shell3.subagent({ name="explorer", description="d", model="haiku", prompt="e" })
local b = shell3.subagent({ name="tester",   description="d", model="haiku", prompt="t" })
shell3.agent({ name="code", model="opus", prompt="c", tools={ subagents={a, b} } })
`)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.FirstAgent().Name != "code" || len(c.Agents()) != 1 {
		t.Fatalf("want single agent code, got %q / %d", c.FirstAgent().Name, len(c.Agents()))
	}
	if len(c.Subagents()) != 2 {
		t.Fatalf("want 2 subagents, got %d", len(c.Subagents()))
	}
}

func TestAgentModelDefaultsToFirstModel(t *testing.T) {
	p := writeConfig(t, twoModelsHdr+`
shell3.agent({ name="build", prompt="b" })
`)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if got := c.FirstAgent().ModelName; got != "opus" {
		t.Fatalf("model fallback = %q, want opus (first declared)", got)
	}
}

// TestAgentByName_LookupAndMiss pins the internal name lookup (agentsetup uses
// it); only one agent can exist, so lookups hit it or miss.
func TestAgentByName_LookupAndMiss(t *testing.T) {
	p := writeConfig(t, `
shell3.model("m", { base_url = "http://x", api_key = "k", model = "mm" })
shell3.agent({ name = "code", model = "m", prompt = "c" })
`)
	c, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	a, ok := c.AgentByName("code")
	if !ok || a.Name != "code" || a.Prompt != "c" {
		t.Fatalf("AgentByName(code) = %+v, %t", a, ok)
	}
	if _, ok := c.AgentByName("nope"); ok {
		t.Fatal("AgentByName(nope) should report ok=false")
	}
	if c.FirstAgent().Name != "code" {
		t.Fatal("FirstAgent should still be code after a failed lookup")
	}
	if got := c.BuildPersonaFor(a); got != "c" {
		t.Fatalf("BuildPersonaFor(code) = %q, want %q", got, "c")
	}
}
