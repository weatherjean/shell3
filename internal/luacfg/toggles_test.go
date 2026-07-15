package luacfg

import "testing"

func TestAgentTogglesParsed(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `
shell3.model("m", { base_url="x", api_key="k", model="y" })
shell3.agent({ name="on", model="m", prompt="p", environment=true, delegation=true })
`)
	c, err := Load(dir + "/shell3.lua")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	on, _ := c.AgentByName("on")
	if !on.Environment || !on.Delegation {
		t.Errorf("on: want both toggles true, got env=%v deleg=%v", on.Environment, on.Delegation)
	}

	dir2 := t.TempDir()
	writeFile(t, dir2, "shell3.lua", `
shell3.model("m", { base_url="x", api_key="k", model="y" })
shell3.agent({ name="off", model="m", prompt="p" })
`)
	c2, err := Load(dir2 + "/shell3.lua")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	off, _ := c2.AgentByName("off")
	if off.Environment || off.Delegation {
		t.Errorf("off: want both toggles false (default), got env=%v deleg=%v", off.Environment, off.Delegation)
	}
}

func TestSubagentTogglesParsed(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `
shell3.model("m", { base_url="x", api_key="k", model="y" })
shell3.agent({ name="a", model="m", prompt="p" })
shell3.subagent({ name="son", description="d", model="m", prompt="p", environment=true, delegation=true })
shell3.subagent({ name="soff", description="d", model="m", prompt="p" })
`)
	c, err := Load(dir + "/shell3.lua")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	on, _ := c.SubagentByName("son")
	if !on.Environment || !on.Delegation {
		t.Errorf("son: want both toggles true, got env=%v deleg=%v", on.Environment, on.Delegation)
	}
	off, _ := c.SubagentByName("soff")
	if off.Environment || off.Delegation {
		t.Errorf("soff: want both toggles false (default), got env=%v deleg=%v", off.Environment, off.Delegation)
	}
}

func TestToolGates_ReadKeysRejected(t *testing.T) {
	for _, key := range []string{"read", "list_files"} {
		dir := t.TempDir()
		writeFile(t, dir, "shell3.lua", `
shell3.model("m", { base_url="x", api_key="k", model="y" })
shell3.agent({ name="a", model="m", prompt="p", tools={ `+key+`=true } })
`)
		_, err := Load(dir + "/shell3.lua")
		if err == nil || !contains(err.Error(), key) {
			t.Fatalf("tools.%s should be rejected, got err=%v", key, err)
		}
	}
}
