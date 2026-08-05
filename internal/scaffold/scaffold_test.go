package scaffold

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/config"
)

// writeEnv writes the .env the rendered config references (empty values are
// fine — api_key is optional under a proxy setup), including the web password
// the template requires and `shell3 boot` always writes.
func writeEnv(t *testing.T, dir string) {
	t.Helper()
	body := "MAIN_API_KEY=\nSHELL3_WEB_PASSWORD=sixteen-characters-long\n"
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
	cfg, err := os.ReadFile(filepath.Join(dir, "shell3.yaml"))
	if err != nil {
		t.Fatalf("read shell3.yaml: %v", err)
	}
	for _, want := range []string{
		"  main:",
		`base_url: "http://localhost:8787/v1"`,
		"api_key: env:MAIN_API_KEY",
		`model: "kimi-k2.6"`,
		`# run_proxy: "npx`,
		"# url: https://shell3.example.com",
	} {
		if !strings.Contains(string(cfg), want) {
			t.Errorf("shell3.yaml missing %q", want)
		}
	}
	if strings.Contains(string(cfg), "{{") {
		t.Errorf("shell3.yaml still contains an unrendered template delimiter")
	}
	// TOTP defaults off: the line stays a commented hint.
	for _, want := range []string{
		"# totp_secret: env:SHELL3_WEB_TOTP_SECRET",
	} {
		if !strings.Contains(string(cfg), want) {
			t.Errorf("shell3.yaml missing commented hint %q", want)
		}
	}
	agentMD, err := os.ReadFile(filepath.Join(dir, "agent.md"))
	if err != nil {
		t.Fatalf("read agent.md: %v", err)
	}
	if !strings.Contains(string(agentMD), "model: main") {
		t.Error("agent.md frontmatter should reference the model")
	}
	// The notifier persona ships with boot: model = the main model, body = the
	// default triage policy.
	notifierMD, err := os.ReadFile(filepath.Join(dir, "notifier.md"))
	if err != nil {
		t.Fatalf("read notifier.md: %v", err)
	}
	if !strings.Contains(string(notifierMD), "model: main") {
		t.Error("notifier.md frontmatter should reference the model")
	}
	if !strings.Contains(string(notifierMD), "send") || !strings.Contains(string(notifierMD), "wake") {
		t.Error("notifier.md body should describe the send/wake verdicts")
	}
	for _, p := range []string{
		"agents/assistant.md",
		"hooks/tool-call.sh",
		"skills/planning.md", "skills/history.md",
		"skills/self-evolve.md", "skills/browser.md", "skills/scripting.md",
		"skills/cookbook.md", "skills/find-skills.md", "skills/writing-code.md",
		"lib/bin/skill-search",
	} {
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			t.Errorf("missing %s: %v", p, err)
		}
	}
}

// TestRenderBaseConfigTOTP verifies an enrolment answer becomes live config:
// enrolled TOTP must render uncommented, or the secret sits unused in .env
// and the login never asks for a code.
func TestRenderBaseConfigTOTP(t *testing.T) {
	dir := t.TempDir()
	v := Values{Name: "main", BaseURL: "http://localhost:1/v1", EnvKey: "K", Model: "m",
		TOTP: true}
	if err := RenderBaseConfig(dir, v, false); err != nil {
		t.Fatalf("RenderBaseConfig: %v", err)
	}
	cfg, err := os.ReadFile(filepath.Join(dir, "shell3.yaml"))
	if err != nil {
		t.Fatalf("read shell3.yaml: %v", err)
	}
	for _, want := range []string{
		"\n  totp_secret: env:SHELL3_WEB_TOTP_SECRET",
	} {
		if !strings.Contains(string(cfg), want) {
			t.Errorf("shell3.yaml missing live line %q", want)
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
		cfg, _ := os.ReadFile(filepath.Join(dir, "shell3.yaml"))
		for _, want := range []string{"context_window: 200000", "compact_at: 150000"} {
			if !strings.Contains(string(cfg), want) {
				t.Errorf("shell3.yaml missing %q", want)
			}
		}
	})

	t.Run("zero values default with compact_at at 80%", func(t *testing.T) {
		dir := t.TempDir()
		v := Values{Name: "main", BaseURL: "http://x/v1", EnvKey: "MAIN_API_KEY", Model: "m"}
		if err := RenderBaseConfig(dir, v, false); err != nil {
			t.Fatalf("RenderBaseConfig: %v", err)
		}
		cfg, _ := os.ReadFile(filepath.Join(dir, "shell3.yaml"))
		// DefaultContextWindow (128000) and 80% of it (102400).
		for _, want := range []string{"context_window: 128000", "compact_at: 102400"} {
			if !strings.Contains(string(cfg), want) {
				t.Errorf("shell3.yaml missing defaulted %q", want)
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
		cfg, _ := os.ReadFile(filepath.Join(dir, "shell3.yaml"))
		if !strings.Contains(string(cfg), "describe: { model: main }") {
			t.Error("vision config should wire media.describe to the main model")
		}
		agentMD, _ := os.ReadFile(filepath.Join(dir, "agent.md"))
		if !strings.Contains(string(agentMD), "tools: [bash, bash_bg, edit, media, history]") {
			t.Error("vision agent.md should enable the media tool")
		}
	})

	t.Run("no vision disables media and keeps describe commented", func(t *testing.T) {
		dir := t.TempDir()
		v := Values{Name: "main", BaseURL: "http://x/v1", EnvKey: "MAIN_API_KEY", Model: "m", Vision: false}
		if err := RenderBaseConfig(dir, v, false); err != nil {
			t.Fatalf("RenderBaseConfig: %v", err)
		}
		cfg, _ := os.ReadFile(filepath.Join(dir, "shell3.yaml"))
		if !strings.Contains(string(cfg), "#   describe: { model: some-vision-model }") {
			t.Error("no-vision config should keep media.describe as a commented hint")
		}
		agentMD, _ := os.ReadFile(filepath.Join(dir, "agent.md"))
		if !strings.Contains(string(agentMD), "tools: [bash, bash_bg, edit, history]") {
			t.Error("no-vision agent.md should not enable the media tool")
		}
	})

	// A vision config must also load: describe references the main model.
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
		defer c.Close()
		if len(c.Warnings()) != 0 {
			t.Errorf("vision config loaded with warnings: %v", c.Warnings())
		}
		if c.Describe() == nil || c.Describe().ModelRef != "main" {
			t.Errorf("describe = %+v, want model main", c.Describe())
		}
	})
}

func TestRenderBaseConfigWithProxy(t *testing.T) {
	dir := t.TempDir()
	v := Values{Name: "main", BaseURL: "http://x/v1", EnvKey: "MAIN_API_KEY", Model: "m", Proxy: "npx codex-proxy --port 8787"}
	if err := RenderBaseConfig(dir, v, false); err != nil {
		t.Fatalf("RenderBaseConfig: %v", err)
	}
	cfg, _ := os.ReadFile(filepath.Join(dir, "shell3.yaml"))
	if !strings.Contains(string(cfg), `run_proxy: "npx codex-proxy --port 8787"`) {
		t.Errorf("proxy not wired into shell3.yaml:\n%s", cfg)
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
	defer c.Close()

	if len(c.Models) < 1 {
		t.Errorf("expected >= 1 model, got %d", len(c.Models))
	}
	a := c.FirstAgent()
	if a.Name != "agent" {
		t.Errorf("agent: want %q, got %q", "agent", a.Name)
	}
	// The agent's skills come from scanning skills/; the load must be
	// warning-free — a skipped skill file in the shipped scaffold is a bug.
	got := map[string]bool{}
	for _, s := range a.Skills {
		got[s.Name] = true
	}
	for _, want := range []string{"planning", "browser", "find-skills", "writing-code", "cookbook", "history", "self-evolve", "scripting"} {
		if !got[want] {
			t.Errorf("scaffold skill %q missing from agent (got %v)", want, got)
		}
	}
	if len(a.Skills) != 8 {
		t.Errorf("expected 8 scaffold skills, got %d", len(a.Skills))
	}
	if len(c.Warnings()) != 0 {
		t.Errorf("scaffold config loaded with warnings: %v", c.Warnings())
	}
	// Subagents are a separate registry; the shipped tree registers assistant,
	// and the main agent's allowlist is inferred from agents/.
	subs := c.Subagents()
	if len(subs) != 1 || subs[0].Name != "assistant" {
		t.Fatalf("expected one registered subagent [assistant], got %v", subs)
	}
	if len(a.Subagents) != 1 || a.Subagents[0] != "assistant" {
		t.Errorf("agent Subagents = %v, want [assistant]", a.Subagents)
	}
	for _, name := range a.Subagents {
		sa, ok := c.SubagentByName(name)
		if !ok {
			t.Fatalf("unresolved subagent %q", name)
		}
		if sa.Description == "" {
			t.Errorf("subagent %q has no description for the task tool schema", name)
		}
	}
	// The shipped hooks are discovered (both scripts are no-op exit 0 gates).
	if !c.HasToolCall() {
		t.Error("scaffold hooks/tool-call.sh not discovered")
	}
}

func TestRenderBaseConfigDoesNotClobber(t *testing.T) {
	dir := t.TempDir()
	v := Values{Name: "main", BaseURL: "http://x/v1", EnvKey: "MAIN_API_KEY", Model: "m"}
	if err := RenderBaseConfig(dir, v, false); err != nil {
		t.Fatalf("first render: %v", err)
	}
	cfgPath := filepath.Join(dir, "shell3.yaml")
	if err := os.WriteFile(cfgPath, []byte("# user edited\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := RenderBaseConfig(dir, v, false); err != nil {
		t.Fatalf("second render: %v", err)
	}
	got, _ := os.ReadFile(cfgPath)
	if string(got) != "# user edited\n" {
		t.Errorf("RenderBaseConfig clobbered an existing shell3.yaml")
	}
}

func TestRenderBaseConfigForceOverwrites(t *testing.T) {
	dir := t.TempDir()
	v := Values{Name: "main", BaseURL: "http://x/v1", EnvKey: "MAIN_API_KEY", Model: "m"}
	if err := RenderBaseConfig(dir, v, false); err != nil {
		t.Fatalf("first render: %v", err)
	}
	cfgPath := filepath.Join(dir, "shell3.yaml")
	if err := os.WriteFile(cfgPath, []byte("# stale\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := RenderBaseConfig(dir, v, true); err != nil {
		t.Fatalf("force render: %v", err)
	}
	got, _ := os.ReadFile(cfgPath)
	if string(got) == "# stale\n" {
		t.Error("force=true did not overwrite shell3.yaml")
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
	defer c.Close()
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

// A scaffolded config must be servable, which means it must reference the
// password key: `shell3 serve` refuses to start without one, so a fresh boot
// that omitted this line would produce a config that cannot run.
func TestBaseConfigWiresTheWebPassword(t *testing.T) {
	dir := t.TempDir()
	if err := RenderBaseConfig(dir, Values{
		Name: "main", BaseURL: "http://x", EnvKey: "K", Model: "m",
		ContextWindow: 100000, CompactAt: 80000, WorkDir: dir,
	}, false); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(dir, "shell3.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "password: env:SHELL3_WEB_PASSWORD") {
		t.Errorf("shell3.yaml does not wire the web password:\n%s", body)
	}
}
