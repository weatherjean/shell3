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

func TestLoadModelRunProxy(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `
shell3.model("proxied", {
  base_url = "http://localhost:8787/v1",
  api_key = "sk-test",
  model = "m-1",
  run_proxy = "litellm --port 8787",
})
shell3.agent({ name="a", model="proxied", prompt="hi", tools={} })
`)
	c, err := Load(dir+"/shell3.lua", dir)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	m, ok := c.Model("proxied")
	if !ok || m.RunProxy != "litellm --port 8787" {
		t.Fatalf("run_proxy not captured: %+v", m)
	}
}

func TestLoadModelDuplicateName(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `
shell3.model("dup", { base_url="u", api_key="k", model="x" })
shell3.model("dup", { base_url="u2", api_key="k2", model="x2" })
shell3.agent({ name="a", model="dup", prompt="p", tools={} })
`)
	_, err := Load(dir+"/shell3.lua", dir)
	if err == nil || !contains(err.Error(), `dup`) {
		t.Fatalf("want duplicate-model error, got %v", err)
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

func TestAgentUnknownKey(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `
shell3.model("m", { base_url="u", api_key="k", model="x" })
shell3.agent({ name="a", model="m", prompt="p", tools={}, bogus=1 })
`)
	_, err := Load(dir+"/shell3.lua", dir)
	if err == nil || !contains(err.Error(), `unknown key "bogus"`) {
		t.Fatalf("want strict-key failure on agent, got %v", err)
	}
}

func TestAgentToolsUnknownKey(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `
shell3.model("m", { base_url="u", api_key="k", model="x" })
shell3.agent({ name="a", model="m", prompt="p", tools={ edt=true } })
`)
	_, err := Load(dir+"/shell3.lua", dir)
	if err == nil || !contains(err.Error(), `unknown key "edt"`) {
		t.Fatalf("want strict-key failure on agent.tools, got %v", err)
	}
}
