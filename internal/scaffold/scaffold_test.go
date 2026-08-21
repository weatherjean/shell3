package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/config"
	"github.com/weatherjean/shell3/internal/kit"
)

// writeEnv writes the .env the rendered config references (empty values are
// fine — api_key is optional under a proxy setup), matching exactly the keys
// `shell3 boot` writes.
func writeEnv(t *testing.T, dir string) {
	t.Helper()
	body := "MAIN_API_KEY=\nTELEGRAM_TOKEN=\n"
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestRenderBaseConfig(t *testing.T) {
	dir := t.TempDir()
	v := Values{Name: "main", BaseURL: "http://localhost:8787/v1", EnvKey: "MAIN_API_KEY", Model: "kimi-k2.6", Proxy: ""}
	if err := RenderBaseConfig(dir, v, false); err != nil {
		t.Fatalf("RenderBaseConfig: %v", err)
	}
	cfg, err := os.ReadFile(filepath.Join(dir, "shell3.sh"))
	if err != nil {
		t.Fatalf("read shell3.sh: %v", err)
	}
	for _, want := range []string{
		"  main:",
		`base_url: "http://localhost:8787/v1"`,
		"api_key: env:MAIN_API_KEY",
		`model: "kimi-k2.6"`,
		`# run_proxy: "npx`,
		"token: env:TELEGRAM_TOKEN",
	} {
		if !strings.Contains(string(cfg), want) {
			t.Errorf("shell3.sh missing %q", want)
		}
	}
	if strings.Contains(string(cfg), "{{") {
		t.Errorf("shell3.sh still contains an unrendered template delimiter")
	}
	agentMD, err := os.ReadFile(filepath.Join(dir, "shell3.sh"))
	if err != nil {
		t.Fatalf("read shell3.sh: %v", err)
	}
	if !strings.Contains(string(agentMD), "model: main") {
		t.Error("shell3.sh should reference the model")
	}
	for _, p := range []string{
		"skills/planning.md", "skills/history.md",
		"skills/self-evolve.md", "skills/browser.md", "skills/scripting.md",
		"skills/cookbook.md", "skills/find-skills.md", "skills/writing-code.md",
		"skills/media.md",
		"lib/bin/skill-search",
	} {
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			t.Errorf("missing %s: %v", p, err)
		}
	}
}

// TestRenderBaseConfigChatID verifies the boot answer becomes live config: the
// chat id renders quoted into the telegram block (or empty when deferred).
func TestRenderBaseConfigChatID(t *testing.T) {
	dir := t.TempDir()
	v := Values{Name: "main", BaseURL: "http://localhost:1/v1", EnvKey: "K", Model: "m",
		ChatID: "123456789"}
	if err := RenderBaseConfig(dir, v, false); err != nil {
		t.Fatalf("RenderBaseConfig: %v", err)
	}
	cfg, err := os.ReadFile(filepath.Join(dir, "shell3.sh"))
	if err != nil {
		t.Fatalf("read shell3.sh: %v", err)
	}
	if !strings.Contains(string(cfg), `chat_id: "123456789"`) {
		t.Errorf("shell3.sh missing quoted chat_id; got:\n%s", cfg)
	}
}

func TestRenderBaseConfigContextWindow(t *testing.T) {
	t.Run("explicit values render through", func(t *testing.T) {
		dir := t.TempDir()
		v := Values{Name: "main", BaseURL: "http://x/v1", EnvKey: "MAIN_API_KEY", Model: "m", ContextWindow: 200000, CompactAt: 150000}
		if err := RenderBaseConfig(dir, v, false); err != nil {
			t.Fatalf("RenderBaseConfig: %v", err)
		}
		cfg, _ := os.ReadFile(filepath.Join(dir, "shell3.sh"))
		for _, want := range []string{"context_window: 200000", "compact_at: 150000"} {
			if !strings.Contains(string(cfg), want) {
				t.Errorf("shell3.sh missing %q", want)
			}
		}
	})

	t.Run("zero values default with compact_at at 80%", func(t *testing.T) {
		dir := t.TempDir()
		v := Values{Name: "main", BaseURL: "http://x/v1", EnvKey: "MAIN_API_KEY", Model: "m"}
		if err := RenderBaseConfig(dir, v, false); err != nil {
			t.Fatalf("RenderBaseConfig: %v", err)
		}
		cfg, _ := os.ReadFile(filepath.Join(dir, "shell3.sh"))
		// DefaultContextWindow (128000) and 80% of it (102400).
		for _, want := range []string{"context_window: 128000", "compact_at: 102400"} {
			if !strings.Contains(string(cfg), want) {
				t.Errorf("shell3.sh missing defaulted %q", want)
			}
		}
	})
}

func TestRenderBaseConfigVision(t *testing.T) {
	t.Run("vision enables the media tool and renders no media: block", func(t *testing.T) {
		dir := t.TempDir()
		v := Values{Name: "main", BaseURL: "http://x/v1", EnvKey: "MAIN_API_KEY", Model: "m", Vision: true}
		if err := RenderBaseConfig(dir, v, false); err != nil {
			t.Fatalf("RenderBaseConfig: %v", err)
		}
		cfg, _ := os.ReadFile(filepath.Join(dir, "shell3.sh"))
		for _, banned := range []string{"media:", "describe:"} {
			if strings.Contains(string(cfg), banned) {
				t.Errorf("vision config should not render %q; got:\n%s", banned, cfg)
			}
		}
		agentMD, _ := os.ReadFile(filepath.Join(dir, "shell3.sh"))
		if !strings.Contains(string(agentMD), "# use: [media]") ||
			strings.Contains(string(agentMD), "# # use: [media]") {
			t.Errorf("vision kit should opt into media; got:\n%s", agentMD)
		}
	})

	t.Run("no vision disables the media tool and renders no media: block", func(t *testing.T) {
		dir := t.TempDir()
		v := Values{Name: "main", BaseURL: "http://x/v1", EnvKey: "MAIN_API_KEY", Model: "m", Vision: false}
		if err := RenderBaseConfig(dir, v, false); err != nil {
			t.Fatalf("RenderBaseConfig: %v", err)
		}
		cfg, _ := os.ReadFile(filepath.Join(dir, "shell3.sh"))
		for _, banned := range []string{"media:", "describe:"} {
			if strings.Contains(string(cfg), banned) {
				t.Errorf("no-vision config should not render %q; got:\n%s", banned, cfg)
			}
		}
		agentMD, _ := os.ReadFile(filepath.Join(dir, "shell3.sh"))
		if !strings.Contains(string(agentMD), "# # use: [media]") {
			t.Errorf("no-vision kit should leave media commented out; got:\n%s", agentMD)
		}
	})

	// A vision config must also load cleanly, with the media tool gated on.
	t.Run("vision config loads", func(t *testing.T) {
		dir := t.TempDir()
		v := Values{Name: "main", BaseURL: "http://x/v1", EnvKey: "MAIN_API_KEY", Model: "m", Vision: true}
		if err := RenderBaseConfig(dir, v, false); err != nil {
			t.Fatalf("RenderBaseConfig: %v", err)
		}
		writeEnv(t, dir)
		c, err := config.Load(dir)
		if err != nil {
			t.Fatalf("vision config failed to load: %v", err)
		}
		if len(c.Warnings()) != 0 {
			t.Errorf("vision config loaded with warnings: %v", c.Warnings())
		}
		src, err := os.ReadFile(filepath.Join(dir, "shell3.sh"))
		if err != nil {
			t.Fatalf("read kit: %v", err)
		}
		k, err := kit.Parse(src)
		if err != nil {
			t.Fatalf("parse kit: %v", err)
		}
		r, err := k.Resolve(k.Agents[0], true)
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		var media bool
		for _, b := range r.Builtins {
			if b == "media" {
				media = true
			}
		}
		if !media {
			t.Errorf("vision config should grant the media tool; builtins = %v", r.Builtins)
		}
	})
}

// TestRenderedYAMLHasNoMediaBlock guards the removal of the media: block from
// the yaml template for good: neither a vision nor a no-vision boot may ever
// emit media:/stt:/tts:/describe:/imagegen: again, but media_keep_days (the
// janitor knob) must survive either way.
func TestRenderedYAMLHasNoMediaBlock(t *testing.T) {
	for _, vision := range []bool{true, false} {
		dir := t.TempDir()
		v := Values{Name: "main", BaseURL: "http://x/v1", EnvKey: "MAIN_API_KEY", Model: "m", Vision: vision}
		if err := RenderBaseConfig(dir, v, false); err != nil {
			t.Fatalf("RenderBaseConfig(vision=%v): %v", vision, err)
		}
		cfg, err := os.ReadFile(filepath.Join(dir, "shell3.sh"))
		if err != nil {
			t.Fatalf("read shell3.sh (vision=%v): %v", vision, err)
		}
		for _, banned := range []string{"media:", "stt:", "tts:", "describe:", "imagegen:"} {
			if strings.Contains(string(cfg), banned) {
				t.Errorf("vision=%v: rendered shell3.sh still contains %q", vision, banned)
			}
		}
		if !strings.Contains(string(cfg), "media_keep_days") {
			t.Errorf("vision=%v: rendered shell3.sh dropped media_keep_days", vision)
		}
	}
}

func TestRenderBaseConfigWithProxy(t *testing.T) {
	dir := t.TempDir()
	v := Values{Name: "main", BaseURL: "http://x/v1", EnvKey: "MAIN_API_KEY", Model: "m", Proxy: "npx codex-proxy --port 8787"}
	if err := RenderBaseConfig(dir, v, false); err != nil {
		t.Fatalf("RenderBaseConfig: %v", err)
	}
	cfg, _ := os.ReadFile(filepath.Join(dir, "shell3.sh"))
	if !strings.Contains(string(cfg), `run_proxy: "npx codex-proxy --port 8787"`) {
		t.Errorf("proxy not wired into shell3.sh:\n%s", cfg)
	}
}

// TestRenderedConfigLoads renders the base config, supplies the .env secrets it
// references, and loads it through the real config loader — verifying the
// shipped templates + files parse and produce the expected agent/tool/skill
// shape. This is the canonical "does our default config work" test.
func TestRenderedConfigLoads(t *testing.T) {
	dir := t.TempDir()
	v := Values{Name: "main", BaseURL: "http://localhost:8787/v1", EnvKey: "MAIN_API_KEY", Model: "test", Proxy: "", Vision: true}
	if err := RenderBaseConfig(dir, v, false); err != nil {
		t.Fatalf("RenderBaseConfig: %v", err)
	}
	// Empty MAIN_API_KEY mirrors a proxy setup (e.g. run_proxy handles auth):
	// the config must still load — api_key is optional.
	writeEnv(t, dir)

	c, err := config.Load(dir)
	if err != nil {
		t.Fatalf("rendered config failed to load with empty api_key: %v", err)
	}

	if len(c.Models) < 1 {
		t.Errorf("expected >= 1 model, got %d", len(c.Models))
	}
	// Under the kit model the agents come from shell3.sh and the skills from
	// scanning skills/ — config.Load only lifts the wiring.
	src, err := os.ReadFile(filepath.Join(dir, "shell3.sh"))
	if err != nil {
		t.Fatalf("read kit: %v", err)
	}
	k, err := kit.Parse(src)
	if err != nil {
		t.Fatalf("parse kit: %v", err)
	}
	if len(k.Agents) != 2 || k.Agents[0].Name != "main" || k.Agents[1].Name != "assistant" {
		t.Fatalf("scaffold kit agents = %+v, want main + assistant", k.Agents)
	}
	if k.Agents[1].Desc == "" {
		t.Error("the assistant employee needs a description — it is how the main agent decides to dispatch it")
	}

	got := map[string]bool{}
	for _, sk := range config.ScanSkills(filepath.Join(dir, "skills")) {
		got[sk.Name] = true
	}
	for _, want := range []string{"planning", "browser", "find-skills", "writing-code",
		"cookbook", "history", "self-evolve", "scripting", "media", "building-agents"} {
		if !got[want] {
			t.Errorf("scaffold skill %q missing (got %v)", want, got)
		}
	}
	if len(c.Warnings()) != 0 {
		t.Errorf("scaffold config loaded with warnings: %v", c.Warnings())
	}
	// The gate ships in the kit now, not as hooks/*.sh — a scaffolded config
	// has no hooks dir at all, so LoadedConfig alone discovers nothing. The
	// gate's own wiring is covered where the kit is loaded (kitagent) and its
	// rules in hooks_test.go.
	if c.HasToolCall() {
		t.Error("a scaffolded config should have no hooks/*.sh — the gate is declared in the kit")
	}
}

func TestRenderBaseConfigDoesNotClobber(t *testing.T) {
	dir := t.TempDir()
	v := Values{Name: "main", BaseURL: "http://x/v1", EnvKey: "MAIN_API_KEY", Model: "m"}
	if err := RenderBaseConfig(dir, v, false); err != nil {
		t.Fatalf("first render: %v", err)
	}
	cfgPath := filepath.Join(dir, "shell3.sh")
	if err := os.WriteFile(cfgPath, []byte("# user edited\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := RenderBaseConfig(dir, v, false); err != nil {
		t.Fatalf("second render: %v", err)
	}
	got, _ := os.ReadFile(cfgPath)
	if string(got) != "# user edited\n" {
		t.Errorf("RenderBaseConfig clobbered an existing shell3.sh")
	}
}

func TestRenderBaseConfigForceOverwrites(t *testing.T) {
	dir := t.TempDir()
	v := Values{Name: "main", BaseURL: "http://x/v1", EnvKey: "MAIN_API_KEY", Model: "m"}
	if err := RenderBaseConfig(dir, v, false); err != nil {
		t.Fatalf("first render: %v", err)
	}
	cfgPath := filepath.Join(dir, "shell3.sh")
	if err := os.WriteFile(cfgPath, []byte("# stale\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := RenderBaseConfig(dir, v, true); err != nil {
		t.Fatalf("force render: %v", err)
	}
	got, _ := os.ReadFile(cfgPath)
	if string(got) == "# stale\n" {
		t.Error("force=true did not overwrite shell3.sh")
	}
	if !strings.Contains(string(got), "base_url:") {
		t.Errorf("force render did not regenerate config; got:\n%s", got)
	}
}

// TestRenderBaseConfigEscapesYAMLSpecials ensures inputs containing YAML
// metacharacters (a quote, a backslash) produce a config that still parses,
// rather than a scalar that closes early.
func TestRenderBaseConfigEscapesYAMLSpecials(t *testing.T) {
	dir := t.TempDir()
	v := Values{
		Name:    "main",
		BaseURL: `http://x/v1" oops: [`, // a quote + YAML specials
		EnvKey:  "MAIN_API_KEY",
		Model:   `weird\model`,     // a backslash
		Proxy:   `sh -c "echo hi"`, // quotes in a proxy command
	}
	if err := RenderBaseConfig(dir, v, false); err != nil {
		t.Fatalf("RenderBaseConfig: %v", err)
	}
	writeEnv(t, dir)
	c, err := config.Load(dir)
	if err != nil {
		t.Fatalf("config with YAML-special inputs failed to load: %v", err)
	}
	// The raw (unescaped) values must round-trip into the loaded model.
	m := c.Models[0]
	if m.BaseURL != v.BaseURL {
		t.Errorf("base_url = %q, want %q", m.BaseURL, v.BaseURL)
	}
	if m.ModelID != v.Model {
		t.Errorf("model = %q, want %q", m.ModelID, v.Model)
	}
	if m.RunProxy != v.Proxy {
		t.Errorf("run_proxy = %q, want %q", m.RunProxy, v.Proxy)
	}
}

// A scaffolded config must be runnable, which means it must reference the
// bot token key: the telegram block resolves env:TELEGRAM_TOKEN at load, so a
// fresh boot that omitted this line would produce a config that cannot run.
func TestBaseConfigWiresTheBotToken(t *testing.T) {
	dir := t.TempDir()
	if err := RenderBaseConfig(dir, Values{
		Name: "main", BaseURL: "http://x", EnvKey: "K", Model: "m",
		ContextWindow: 100000, CompactAt: 80000, WorkDir: dir,
	}, false); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "shell3.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "token: env:TELEGRAM_TOKEN") {
		t.Errorf("shell3.sh does not wire the bot token:\n%s", body)
	}
}

// The kit template ships a commented cron: example. A declaration block is
// itself a comment fence, so an example written with real #--- fences would
// not be an example — it would be an armed job on every fresh boot. The
// example's fences are ##---, and this pins that they stay inert.
func TestScaffoldCronExampleIsInert(t *testing.T) {
	dir := t.TempDir()
	v := Values{Name: "main", BaseURL: "http://localhost:8787/v1", EnvKey: "MAIN_API_KEY", Model: "test"}
	if err := RenderBaseConfig(dir, v, false); err != nil {
		t.Fatalf("RenderBaseConfig: %v", err)
	}
	src, err := os.ReadFile(filepath.Join(dir, "shell3.sh"))
	if err != nil {
		t.Fatalf("read kit: %v", err)
	}
	if !strings.Contains(string(src), "cron: checklist") {
		t.Fatal("the kit template should carry a commented cron: example")
	}
	k, err := kit.Parse(src)
	if err != nil {
		t.Fatalf("parse kit: %v", err)
	}
	if len(k.Crons) != 0 {
		t.Fatalf("the commented example armed %d job(s): %+v", len(k.Crons), k.Crons)
	}
}
