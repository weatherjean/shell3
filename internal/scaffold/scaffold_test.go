package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/luacfg"
)

func TestRenderBaseConfig(t *testing.T) {
	dir := t.TempDir()
	v := Values{Name: "main", BaseURL: "http://localhost:8787/v1", EnvKey: "MAIN_API_KEY", Model: "kimi-k2.6", Proxy: ""}
	if err := RenderBaseConfig(dir, v, false); err != nil {
		t.Fatalf("RenderBaseConfig: %v", err)
	}
	cfg, err := os.ReadFile(filepath.Join(dir, "shell3.lua"))
	if err != nil {
		t.Fatalf("read shell3.lua: %v", err)
	}
	for _, want := range []string{
		`shell3.model("main"`,
		`base_url       = "http://localhost:8787/v1"`,
		`shell3.env.secret("MAIN_API_KEY")`,
		`model          = "kimi-k2.6"`,
		`name  = "code"`,
		`name  = "plan"`,
		`-- run_proxy   = "npx`,
	} {
		if !strings.Contains(string(cfg), want) {
			t.Errorf("shell3.lua missing %q", want)
		}
	}
	if !strings.Contains(string(cfg), "subagents") {
		t.Error("rendered code agent should enable subagents")
	}
	if !strings.Contains(string(cfg), "shell3.subagent(") {
		t.Error("rendered config should declare an example subagent via shell3.subagent(")
	}
	if !strings.Contains(string(cfg), "confirm_destructive") {
		t.Error("rendered config should wire the confirm_destructive ask guard")
	}
	if strings.Contains(string(cfg), "{{") {
		t.Errorf("shell3.lua still contains an unrendered template delimiter")
	}
	for _, p := range []string{
		"lib/tools.lua", "lib/guards.lua",
		"lib/skills/brainstorming.lua",
	} {
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			t.Errorf("missing %s: %v", p, err)
		}
	}
}

func TestRenderBaseConfigWithProxy(t *testing.T) {
	dir := t.TempDir()
	v := Values{Name: "main", BaseURL: "http://x/v1", EnvKey: "MAIN_API_KEY", Model: "m", Proxy: "npx codex-proxy --port 8787"}
	if err := RenderBaseConfig(dir, v, false); err != nil {
		t.Fatalf("RenderBaseConfig: %v", err)
	}
	cfg, _ := os.ReadFile(filepath.Join(dir, "shell3.lua"))
	if !strings.Contains(string(cfg), `run_proxy      = "npx codex-proxy --port 8787"`) {
		t.Errorf("proxy not wired into shell3.lua:\n%s", cfg)
	}
}

// TestRenderedConfigLoads renders the base config, supplies the .env secrets it
// references, and loads it through the real luacfg loader — verifying the
// shipped template + lib modules parse and produce the expected agent/tool/skill
// shape. This is the canonical "does our default config work" test.
func TestRenderedConfigLoads(t *testing.T) {
	dir := t.TempDir()
	v := Values{Name: "main", BaseURL: "http://localhost:8787/v1", EnvKey: "MAIN_API_KEY", Model: "test", Proxy: ""}
	if err := RenderBaseConfig(dir, v, false); err != nil {
		t.Fatalf("RenderBaseConfig: %v", err)
	}
	// Empty MAIN_API_KEY mirrors a proxy setup (e.g. run_proxy handles auth):
	// the config must still load — api_key is optional.
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("MAIN_API_KEY=\nBRAVE_API_KEY=\n"), 0600); err != nil {
		t.Fatal(err)
	}

	c, err := luacfg.Load(filepath.Join(dir, "shell3.lua"), dir)
	if err != nil {
		t.Fatalf("rendered config failed to load with empty api_key: %v", err)
	}
	defer c.Close()

	if len(c.Models) < 1 {
		t.Errorf("expected >= 1 model, got %d", len(c.Models))
	}
	if len(c.Tools) != 2 {
		t.Errorf("expected 2 tools (web_fetch, brave_search), got %d", len(c.Tools))
	}
	if len(c.Skills) != 2 {
		t.Errorf("expected 2 skills (brainstorming, self-evolve), got %d", len(c.Skills))
	}
	agents := c.Agents()
	if len(agents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(agents))
	}
	if agents[0].Name != "code" {
		t.Errorf("first agent: want %q, got %q", "code", agents[0].Name)
	}
	if agents[1].Name != "plan" {
		t.Errorf("second agent: want %q, got %q", "plan", agents[1].Name)
	}
	// Subagents are a separate registry, not in the Tab rotation.
	subs := c.Subagents()
	if len(subs) != 1 || subs[0].Name != "explorer" {
		t.Fatalf("expected one registered subagent [explorer], got %v", subs)
	}
	if _, ok := c.SubagentByName("explorer"); !ok {
		t.Error("explorer must be in the subagent registry")
	}
	for _, a := range agents {
		if a.Name == "explorer" {
			t.Error("explorer must NOT appear in Agents()/Tab rotation")
		}
	}
	// The code agent opts into explorer; plan does not.
	if len(agents[0].Subagents) != 1 || agents[0].Subagents[0] != "explorer" {
		t.Errorf("code agent Subagents = %v, want [explorer]", agents[0].Subagents)
	}
	if len(agents[1].Subagents) != 0 {
		t.Errorf("plan agent should have no subagents, got %v", agents[1].Subagents)
	}
	// The code agent's resolved schema exposes spawn_agent with explorer in its enum.
	infos := make([]luacfg.SubagentInfo, 0, len(agents[0].Subagents))
	for _, name := range agents[0].Subagents {
		sa, ok := c.SubagentByName(name)
		if !ok {
			t.Fatalf("unresolved subagent %q", name)
		}
		infos = append(infos, luacfg.SubagentInfo{Name: sa.Name, Description: sa.Description})
	}
	defs := luacfg.SpawnToolDefs(infos)
	var sawSpawn bool
	for _, d := range defs {
		if d.Name == "spawn_agent" {
			sawSpawn = true
			props, ok := d.Parameters["properties"].(map[string]any)
			if !ok {
				t.Fatal("spawn_agent schema missing properties map")
			}
			subProp, ok := props["subagent"].(map[string]any)
			if !ok {
				t.Fatal("spawn_agent schema missing subagent property")
			}
			enum, ok := subProp["enum"].([]string)
			if !ok {
				t.Fatalf("subagent enum has unexpected type %T", subProp["enum"])
			}
			if len(enum) != 1 || enum[0] != "explorer" {
				t.Errorf("spawn_agent subagent enum = %v, want [explorer]", enum)
			}
		}
	}
	if !sawSpawn {
		t.Error("code agent schema should expose spawn_agent")
	}
}

func TestRenderBaseConfigDoesNotClobber(t *testing.T) {
	dir := t.TempDir()
	v := Values{Name: "main", BaseURL: "http://x/v1", EnvKey: "MAIN_API_KEY", Model: "m"}
	if err := RenderBaseConfig(dir, v, false); err != nil {
		t.Fatalf("first render: %v", err)
	}
	cfgPath := filepath.Join(dir, "shell3.lua")
	if err := os.WriteFile(cfgPath, []byte("-- user edited\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := RenderBaseConfig(dir, v, false); err != nil {
		t.Fatalf("second render: %v", err)
	}
	got, _ := os.ReadFile(cfgPath)
	if string(got) != "-- user edited\n" {
		t.Errorf("RenderBaseConfig clobbered an existing shell3.lua")
	}
}

func TestRenderBaseConfigForceOverwrites(t *testing.T) {
	dir := t.TempDir()
	v := Values{Name: "main", BaseURL: "http://x/v1", EnvKey: "MAIN_API_KEY", Model: "m"}
	if err := RenderBaseConfig(dir, v, false); err != nil {
		t.Fatalf("first render: %v", err)
	}
	cfgPath := filepath.Join(dir, "shell3.lua")
	if err := os.WriteFile(cfgPath, []byte("-- stale\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := RenderBaseConfig(dir, v, true); err != nil {
		t.Fatalf("force render: %v", err)
	}
	got, _ := os.ReadFile(cfgPath)
	if string(got) == "-- stale\n" {
		t.Error("force=true did not overwrite shell3.lua")
	}
	if !strings.Contains(string(got), `shell3.model("main"`) {
		t.Errorf("force render did not regenerate config; got:\n%s", got)
	}
}

// TestRenderBaseConfigEscapesLuaSpecials ensures inputs containing Lua string
// metacharacters (a quote, a backslash) produce a config that still parses,
// rather than a literal that closes early or an invalid escape.
func TestRenderBaseConfigEscapesLuaSpecials(t *testing.T) {
	dir := t.TempDir()
	v := Values{
		Name:    "main",
		BaseURL: `http://x/v1"]] end --`, // a quote + bracket that would break a raw literal
		EnvKey:  "MAIN_API_KEY",
		Model:   `weird\model`,     // a backslash → invalid Lua escape if unescaped
		Proxy:   `sh -c "echo hi"`, // quotes in a proxy command
	}
	if err := RenderBaseConfig(dir, v, false); err != nil {
		t.Fatalf("RenderBaseConfig: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("MAIN_API_KEY=x\nBRAVE_API_KEY=\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := luacfg.Load(filepath.Join(dir, "shell3.lua"), dir)
	if err != nil {
		t.Fatalf("config with Lua-special inputs failed to load: %v", err)
	}
	defer c.Close()
	// The raw (unescaped) values must round-trip into the loaded model.
	m := c.Models[0]
	if m.BaseURL != v.BaseURL {
		t.Errorf("base_url = %q, want %q", m.BaseURL, v.BaseURL)
	}
	if m.ModelID != v.Model {
		t.Errorf("model = %q, want %q", m.ModelID, v.Model)
	}
}
