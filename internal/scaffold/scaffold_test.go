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
	if strings.Contains(string(cfg), "{{") {
		t.Errorf("shell3.lua still contains an unrendered template delimiter")
	}
	for _, p := range []string{
		"lib/tools.lua", "lib/guards.lua",
		"lib/skills/brainstorming.lua", "lib/skills/subagents.lua",
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
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("MAIN_API_KEY=x\nBRAVE_API_KEY=\n"), 0600); err != nil {
		t.Fatal(err)
	}

	c, err := luacfg.Load(filepath.Join(dir, "shell3.lua"), dir)
	if err != nil {
		t.Fatalf("rendered config failed to load: %v", err)
	}
	defer c.Close()

	if len(c.Models) < 1 {
		t.Errorf("expected >= 1 model, got %d", len(c.Models))
	}
	if len(c.Tools) != 2 {
		t.Errorf("expected 2 tools (web_fetch, brave_search), got %d", len(c.Tools))
	}
	if len(c.Skills) != 2 {
		t.Errorf("expected 2 skills (brainstorming, spawning-subagents), got %d", len(c.Skills))
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
		BaseURL: `http://x/v1"]] end --`,        // a quote + bracket that would break a raw literal
		EnvKey:  "MAIN_API_KEY",
		Model:   `weird\model`,                  // a backslash → invalid Lua escape if unescaped
		Proxy:   `sh -c "echo hi"`,              // quotes in a proxy command
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
