package luacfg

import (
	"testing"
)

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
	c, err := Load(dir + "/shell3.lua")
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
	c, err := Load(dir + "/shell3.lua")
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
	_, err := Load(dir + "/shell3.lua")
	if err == nil || !contains(err.Error(), `dup`) {
		t.Fatalf("want duplicate-model error, got %v", err)
	}
}

func TestLoadModelUnknownKey(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "shell3.lua", `shell3.model("m", { base_url="u", api_key="k", model="x", nope=1 })`)
	_, err := Load(dir + "/shell3.lua")
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
	_, err := Load(dir + "/shell3.lua")
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
	_, err := Load(dir + "/shell3.lua")
	if err == nil || !contains(err.Error(), `unknown key "edt"`) {
		t.Fatalf("want strict-key failure on agent.tools, got %v", err)
	}
}

func TestModel_KeepRecentParsed(t *testing.T) {
	cfg := mustLoad(t, `
shell3.model("m", { base_url="http://x", model="m", context_window=1000, compact_at=800, keep_recent=300 })
shell3.agent({ name="a", model="m", prompt="p", tools={ bash=true } })
`)
	m, ok := cfg.Model("m")
	if !ok {
		t.Fatal("model m not found")
	}
	if m.KeepRecent != 300 {
		t.Fatalf("KeepRecent = %d, want 300", m.KeepRecent)
	}
}

func TestModel_KeepRecentClampedBelowCompactAt(t *testing.T) {
	cfg := mustLoad(t, `
shell3.model("m", { base_url="http://x", model="m", context_window=1000, compact_at=800, keep_recent=900 })
shell3.agent({ name="a", model="m", prompt="p", tools={ bash=true } })
`)
	// keep_recent (900) >= compact_at (800) is nonsensical (tail never shrinks);
	// it must be clamped to round(compact_at*0.5) = 400.
	m, ok := cfg.Model("m")
	if !ok {
		t.Fatal("model m not found")
	}
	if got := m.KeepRecent; got != 400 {
		t.Fatalf("KeepRecent = %d, want clamped 400", got)
	}
}

func TestModel_PruneAtParsed(t *testing.T) {
	cfg := mustLoad(t, `
shell3.model("m", { base_url="http://x", model="m", context_window=1000, compact_at=800, prune_at=400 })
shell3.agent({ name="a", model="m", prompt="p", tools={ bash=true } })
`)
	m, ok := cfg.Model("m")
	if !ok {
		t.Fatal("model m not found")
	}
	if m.PruneAt != 400 {
		t.Fatalf("PruneAt = %d, want 400", m.PruneAt)
	}
}

func TestModel_PruneAtClampedAtOrAboveCompactAt(t *testing.T) {
	cfg := mustLoad(t, `
shell3.model("m", { base_url="http://x", model="m", context_window=1000, compact_at=800, prune_at=800 })
shell3.agent({ name="a", model="m", prompt="p", tools={ bash=true } })
`)
	// prune_at (800) >= compact_at (800) is pointless; must be clamped to 0 (disabled).
	m, ok := cfg.Model("m")
	if !ok {
		t.Fatal("model m not found")
	}
	if got := m.PruneAt; got != 0 {
		t.Fatalf("PruneAt = %d, want clamped 0", got)
	}
}

func TestModel_PruneAtDefaultsToFractionOfCompactAt(t *testing.T) {
	cfg := mustLoad(t, `
shell3.model("m", { base_url="http://x", model="m", context_window=1000, compact_at=800 })
shell3.agent({ name="a", model="m", prompt="p", tools={ bash=true } })
`)
	// Omitting prune_at must default it to round(compact_at*0.6) = 480 so the
	// cheap-prune tier is on by default (this is the regression the missing
	// default produced: pruning silently disabled despite the docs).
	m, ok := cfg.Model("m")
	if !ok {
		t.Fatal("model m not found")
	}
	if got := m.PruneAt; got != 480 {
		t.Fatalf("PruneAt = %d, want defaulted 480", got)
	}
}

func TestModel_PruneAtExplicitZeroDisables(t *testing.T) {
	cfg := mustLoad(t, `
shell3.model("m", { base_url="http://x", model="m", context_window=1000, compact_at=800, prune_at=0 })
shell3.agent({ name="a", model="m", prompt="p", tools={ bash=true } })
`)
	// An explicit prune_at=0 means "disable this tier" and must NOT be rewritten
	// to the default (unset vs explicit-0 are distinguished at parse time).
	m, ok := cfg.Model("m")
	if !ok {
		t.Fatal("model m not found")
	}
	if got := m.PruneAt; got != 0 {
		t.Fatalf("PruneAt = %d, want explicit 0 (disabled)", got)
	}
}
