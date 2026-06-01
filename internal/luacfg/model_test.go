package luacfg

import "testing"

func TestLoadModel(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `
shell3.model("main", {
  base_url = "https://api.x/v1",
  api_key = "sk-test",
  model = "m-1",
  context_window = 1000,
  reasoning = "medium",
  extra = { verbosity = "high" },
})
shell3.agent({ name="a", model="main", prompt="hi", tools={} })
`)
	c, err := Load(dir+"/shell3.lua", dir)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	m, ok := c.Model("main")
	if !ok || m.BaseURL != "https://api.x/v1" || m.ModelID != "m-1" ||
		m.ContextWindow != 1000 || m.Reasoning != "medium" {
		t.Fatalf("bad model: %+v", m)
	}
	if m.Extra["verbosity"] != "high" {
		t.Fatalf("extra not captured: %+v", m.Extra)
	}
}

func TestLoadModelUnknownKey(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `shell3.model("m", { base_url="u", api_key="k", model="x", nope=1 })`)
	_, err := Load(dir+"/shell3.lua", dir)
	if err == nil || !contains(err.Error(), `unknown key "nope"`) {
		t.Fatalf("want strict-key failure, got %v", err)
	}
}
