package luacfg

import "testing"

func TestSecret(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, ".env", "API=topsecret\n")
	writeFile(t, dir, "shell3.lua", `
shell3.model("m", { base_url="u", api_key=shell3.env.secret("API"), model="x" })
shell3.agent({ name="a", model="m", prompt="p", tools={} })
`)
	c, err := Load(dir + "/shell3.lua")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	m, _ := c.Model("m")
	if m.APIKey != "topsecret" {
		t.Fatalf("secret not resolved: %q", m.APIKey)
	}
}

func TestSecretMissing(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `shell3.env.secret("NOPE")`)
	_, err := Load(dir + "/shell3.lua")
	if err == nil || !contains(err.Error(), "NOPE") {
		t.Fatalf("want missing-secret error, got %v", err)
	}
}

// TestSubagentsGlobalRemoved pins the clean break: shell3.subagents{max_depth}
// no longer exists (single-level delegation is enforced by construction), so
// calling it raises a nil-value error.
func TestSubagentsGlobalRemoved(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `
shell3.model("main", { base_url="u", api_key="k", model="x" })
shell3.agent({ name="a", model="main", prompt="p", tools={} })
shell3.subagents({ max_depth = 2 })
`)
	_, err := Load(dir + "/shell3.lua")
	if err == nil || !contains(err.Error(), "attempt to call a non-function object") {
		t.Fatalf("want call-error for removed shell3.subagents, got %v", err)
	}
}
