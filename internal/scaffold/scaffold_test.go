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
		`-- run_proxy   = "npx`,
	} {
		if !strings.Contains(string(cfg), want) {
			t.Errorf("shell3.lua missing %q", want)
		}
	}
	if !strings.Contains(string(cfg), "subagents") {
		t.Error("rendered code agent should enable subagents")
	}
	if !strings.Contains(string(cfg), `tunnel  = "cloudflared tunnel --url http://{addr}"`) {
		t.Error("rendered dashboard should default to the cloudflared tunnel")
	}
	if !strings.Contains(string(cfg), "shell3.subagent(") {
		t.Error("rendered config should declare an example subagent via shell3.subagent(")
	}
	if !strings.Contains(string(cfg), "shell3.on_tool_call") {
		t.Error("rendered config should document the shell3.on_tool_call command gate")
	}
	if strings.Contains(string(cfg), "{{") {
		t.Errorf("shell3.lua still contains an unrendered template delimiter")
	}
	for _, p := range []string{
		"lib/tools.lua",
		"lib/skills/brainstorming.md", "lib/skills/history.md",
		"lib/skills/self-evolve.md", "lib/skills/browser.md",
	} {
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			t.Errorf("missing %s: %v", p, err)
		}
	}
}

func TestRenderBaseConfigContextWindow(t *testing.T) {
	t.Run("explicit values render through", func(t *testing.T) {
		dir := t.TempDir()
		v := Values{Name: "main", BaseURL: "http://x/v1", EnvKey: "MAIN_API_KEY", Model: "m", ContextWindow: 200000, CompactAt: 150000}
		if err := RenderBaseConfig(dir, v, false); err != nil {
			t.Fatalf("RenderBaseConfig: %v", err)
		}
		cfg, _ := os.ReadFile(filepath.Join(dir, "shell3.lua"))
		for _, want := range []string{"context_window = 200000", "compact_at     = 150000"} {
			if !strings.Contains(string(cfg), want) {
				t.Errorf("shell3.lua missing %q", want)
			}
		}
	})

	t.Run("zero values default with compact_at at 80%", func(t *testing.T) {
		dir := t.TempDir()
		v := Values{Name: "main", BaseURL: "http://x/v1", EnvKey: "MAIN_API_KEY", Model: "m"}
		if err := RenderBaseConfig(dir, v, false); err != nil {
			t.Fatalf("RenderBaseConfig: %v", err)
		}
		cfg, _ := os.ReadFile(filepath.Join(dir, "shell3.lua"))
		// DefaultContextWindow (128000) and 80% of it (102400).
		for _, want := range []string{"context_window = 128000", "compact_at     = 102400"} {
			if !strings.Contains(string(cfg), want) {
				t.Errorf("shell3.lua missing defaulted %q", want)
			}
		}
	})
}

func TestRenderBaseConfigVision(t *testing.T) {
	t.Run("vision wires describe to the main model and enables media", func(t *testing.T) {
		dir := t.TempDir()
		v := Values{Name: "main", BaseURL: "http://x/v1", EnvKey: "MAIN_API_KEY", Model: "m", Vision: true}
		if err := RenderBaseConfig(dir, v, false); err != nil {
			t.Fatalf("RenderBaseConfig: %v", err)
		}
		cfg, _ := os.ReadFile(filepath.Join(dir, "shell3.lua"))
		for _, want := range []string{
			`shell3.describe{ model = "main" }`,
			"media             = true,",
		} {
			if !strings.Contains(string(cfg), want) {
				t.Errorf("shell3.lua missing %q", want)
			}
		}
		if strings.Contains(string(cfg), "media             = false") {
			t.Error("vision config must not disable the media tool")
		}
	})

	t.Run("no vision disables media and keeps describe commented", func(t *testing.T) {
		dir := t.TempDir()
		v := Values{Name: "main", BaseURL: "http://x/v1", EnvKey: "MAIN_API_KEY", Model: "m", Vision: false}
		if err := RenderBaseConfig(dir, v, false); err != nil {
			t.Fatalf("RenderBaseConfig: %v", err)
		}
		cfg, _ := os.ReadFile(filepath.Join(dir, "shell3.lua"))
		if !strings.Contains(string(cfg), "media             = false,") {
			t.Error("no-vision config should render media = false")
		}
		if !strings.Contains(string(cfg), "-- shell3.describe{") {
			t.Error("no-vision config should keep shell3.describe as a commented hint")
		}
		if strings.Contains(string(cfg), "\nshell3.describe{") {
			t.Error("no-vision config must not activate shell3.describe")
		}
	})

	// A vision config must also load: describe references the main model.
	t.Run("vision config loads", func(t *testing.T) {
		dir := t.TempDir()
		v := Values{Name: "main", BaseURL: "http://x/v1", EnvKey: "MAIN_API_KEY", Model: "m", Vision: true}
		if err := RenderBaseConfig(dir, v, false); err != nil {
			t.Fatalf("RenderBaseConfig: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("MAIN_API_KEY=\nBRAVE_API_KEY=\nTELEGRAM_BOT_TOKEN=\nSHELL3_WEB_SECRET=s\n"), 0600); err != nil {
			t.Fatal(err)
		}
		c, err := luacfg.Load(filepath.Join(dir, "shell3.lua"))
		if err != nil {
			t.Fatalf("vision config failed to load: %v", err)
		}
		defer c.Close()
		if len(c.Warnings()) != 0 {
			t.Errorf("vision config loaded with warnings: %v", c.Warnings())
		}
	})
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
	v := Values{Name: "main", BaseURL: "http://localhost:8787/v1", EnvKey: "MAIN_API_KEY", Model: "test", Proxy: "", Vision: true}
	if err := RenderBaseConfig(dir, v, false); err != nil {
		t.Fatalf("RenderBaseConfig: %v", err)
	}
	// Empty MAIN_API_KEY mirrors a proxy setup (e.g. run_proxy handles auth):
	// the config must still load — api_key is optional.
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("MAIN_API_KEY=\nBRAVE_API_KEY=\nTELEGRAM_BOT_TOKEN=\nSHELL3_WEB_SECRET=s\n"), 0600); err != nil {
		t.Fatal(err)
	}

	c, err := luacfg.Load(filepath.Join(dir, "shell3.lua"))
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
	// Both base tools are bash command templates (no Lua handler): each must
	// register with a non-empty Command.
	cmds := map[string]string{}
	for _, tl := range c.Tools {
		cmds[tl.Name] = tl.Command
	}
	for _, name := range []string{"web_fetch", "brave_search"} {
		cmd, ok := cmds[name]
		if !ok {
			t.Errorf("custom tool %q not registered", name)
			continue
		}
		if strings.TrimSpace(cmd) == "" {
			t.Errorf("custom tool %q has an empty Command", name)
		}
	}
	agents := c.Agents()
	if len(agents) != 1 {
		t.Fatalf("expected 1 agent, got %d", len(agents))
	}
	if agents[0].Name != "code" {
		t.Errorf("agent: want %q, got %q", "code", agents[0].Name)
	}
	// The agent's skills come from scanning lib/skills/ (dir-based, no Lua
	// declarations); the load must be warning-free — a skipped skill file in
	// the shipped scaffold is a bug.
	got := map[string]bool{}
	for _, s := range agents[0].Skills {
		got[s.Name] = true
	}
	for _, want := range []string{"brainstorming", "browser", "history", "self-evolve"} {
		if !got[want] {
			t.Errorf("scaffold skill %q missing from agent (got %v)", want, got)
		}
	}
	if len(agents[0].Skills) != 4 {
		t.Errorf("expected 4 scaffold skills, got %d", len(agents[0].Skills))
	}
	if len(c.Warnings()) != 0 {
		t.Errorf("scaffold config loaded with warnings: %v", c.Warnings())
	}
	// Subagents are a separate registry.
	subs := c.Subagents()
	if len(subs) != 1 || subs[0].Name != "explorer" {
		t.Fatalf("expected one registered subagent [explorer], got %v", subs)
	}
	if _, ok := c.SubagentByName("explorer"); !ok {
		t.Error("explorer must be in the subagent registry")
	}
	for _, a := range agents {
		if a.Name == "explorer" {
			t.Error("explorer must NOT appear in Agents()")
		}
	}
	// The code agent opts into explorer.
	if len(agents[0].Subagents) != 1 || agents[0].Subagents[0] != "explorer" {
		t.Errorf("code agent Subagents = %v, want [explorer]", agents[0].Subagents)
	}
	// Each subagent the code agent may delegate to resolves to a (name,
	// description) pair — the raw material baked into the task tool's
	// subagent_type schema (luacfg.TaskToolFor). (Delegation runs through the
	// `task` tool as an in-process background job.)
	for _, name := range agents[0].Subagents {
		sa, ok := c.SubagentByName(name)
		if !ok {
			t.Fatalf("unresolved subagent %q", name)
		}
		if sa.Description == "" {
			t.Errorf("subagent %q has no description for the task tool schema", name)
		}
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
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("MAIN_API_KEY=x\nBRAVE_API_KEY=\nTELEGRAM_BOT_TOKEN=\nSHELL3_WEB_SECRET=s\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := luacfg.Load(filepath.Join(dir, "shell3.lua"))
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
