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
	if got := c.Active().Name; got != "build" {
		t.Fatalf("active = %q, want build (first declared)", got)
	}
	names := []string{}
	for _, a := range c.Agents() {
		names = append(names, a.Name)
	}
	if len(names) != 2 || names[0] != "build" || names[1] != "plan" {
		t.Fatalf("agent order = %v, want [build plan]", names)
	}
}

func TestSwitchAgentByName(t *testing.T) {
	p := writeConfig(t, twoModelsHdr+`
shell3.agent({ name="build", model="opus",  prompt="b" })
shell3.agent({ name="plan",  model="haiku", prompt="p" })
`)
	c, err := Load(p, filepath.Dir(p))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	a, err := c.SwitchAgent("plan")
	if err != nil || a.Name != "plan" {
		t.Fatalf("SwitchAgent(plan) = %v, %v", a.Name, err)
	}
	if c.Active().Name != "plan" {
		t.Fatalf("active after switch = %q, want plan", c.Active().Name)
	}
	if _, err := c.SwitchAgent("nope"); err == nil {
		t.Fatal("SwitchAgent(nope) should error")
	}
	if c.Active().Name != "plan" {
		t.Fatal("failed switch must leave active unchanged")
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
	if got := c.Active().ModelName; got != "opus" {
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
	if c.Active().Name != "base" || len(c.Agents()) != 1 {
		t.Fatalf("single-agent back-compat broken: %q / %d", c.Active().Name, len(c.Agents()))
	}
}
