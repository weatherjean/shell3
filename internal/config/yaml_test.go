package config

import (
	"strings"
	"testing"
)

func parseY(t *testing.T, yamlText string, secrets map[string]string) (*LoadedConfig, error) {
	t.Helper()
	c := &LoadedConfig{}
	return c, c.parseYAML([]byte(yamlText), secrets)
}

const fullYAML = `models:
  main:
    base_url: https://api.deepseek.com/v1
    api_key: env:DEEPSEEK_API_KEY
    model: deepseek-chat
    context_window: 128000
    compact_at: 100000
    prune_at: 60000
    reasoning: medium
    extra: { reasoning_split: true }
    run_proxy: "npx proxy --port 1"
  aux:
    base_url: https://api.groq.com/openai/v1
    api_key: k2
    model: whisper-large-v3-turbo
telegram:
  token: env:TG_TOKEN
  chat_id: "42"
  dashboard:
    addr: 127.0.0.1:8765
    tunnel: cloudflared tunnel --url http://{addr}
web:
  addr: 127.0.0.1:8787
  secret: env:WEB_SECRET
mcp:
  linear:
    url: https://mcp.linear.app/mcp
    headers: { Authorization: "Bearer env:LINEAR_KEY" }
    allow: [search_issues]
  github:
    command: [github-mcp-server, stdio]
    env: { GITHUB_TOKEN: env:GH }
media:
  stt: { model: aux }
  describe: { model: main }
background:
  max_concurrent: 4
`

var fullSecrets = map[string]string{
	"DEEPSEEK_API_KEY": "sk-1", "TG_TOKEN": "tok", "WEB_SECRET": "ws",
	"LINEAR_KEY": "lk", "GH": "gh",
}

func TestParseYAMLFull(t *testing.T) {
	c, err := parseY(t, fullYAML, fullSecrets)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Models) != 2 || c.Models[0].Name != "aux" || c.Models[1].Name != "main" {
		t.Fatalf("models = %+v", c.Models)
	}
	m, _ := c.Model("main")
	if m.APIKey != "sk-1" || m.ModelID != "deepseek-chat" || m.CompactAt != 100000 || m.PruneAt != 60000 {
		t.Fatalf("main = %+v", m)
	}
	if m.Extra["reasoning_split"] != true {
		t.Fatalf("extra = %+v", m.Extra)
	}
	if tg := c.Telegram(); tg.Token != "tok" || tg.ChatID != "42" || !tg.Dashboard.Enabled || tg.Dashboard.Addr != "127.0.0.1:8765" {
		t.Fatalf("telegram = %+v", tg)
	}
	if w := c.Web(); w.Secret != "ws" {
		t.Fatalf("web = %+v", w)
	}
	servers := c.MCPServers()
	if len(servers) != 2 || servers[0].Name != "github" || servers[1].Name != "linear" {
		t.Fatalf("mcp = %+v", servers)
	}
	if servers[1].Headers["Authorization"] != "Bearer lk" {
		t.Fatalf("header substitution failed: %+v", servers[1].Headers)
	}
	if servers[0].Env["GITHUB_TOKEN"] != "gh" {
		t.Fatalf("mcp env substitution failed: %+v", servers[0].Env)
	}
	if c.STT() == nil || c.STT().ModelRef != "aux" || c.Describe() == nil {
		t.Fatal("media blocks missing")
	}
	if c.BackgroundMaxConcurrent != 4 {
		t.Fatalf("background = %d", c.BackgroundMaxConcurrent)
	}
}

func TestParseYAMLUnknownKey(t *testing.T) {
	_, err := parseY(t, "models:\n  m:\n    base_url: u\n    model: x\n    bogus: 1\n", nil)
	if err == nil || !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("err = %v", err)
	}
	_, err = parseY(t, "modelz: {}\n", nil)
	if err == nil {
		t.Fatal("unknown top-level key accepted")
	}
}

func TestParseYAMLNoModels(t *testing.T) {
	if _, err := parseY(t, "web: { addr: x, secret: s }\n", nil); err == nil || !strings.Contains(err.Error(), "no models") {
		t.Fatalf("err = %v", err)
	}
}

func TestParseYAMLUnknownEnvKey(t *testing.T) {
	_, err := parseY(t, "models:\n  m:\n    base_url: u\n    model: x\n    api_key: env:MISSING\n", map[string]string{})
	if err == nil || !strings.Contains(err.Error(), "env:MISSING") {
		t.Fatalf("err = %v", err)
	}
}

func TestParseYAMLMCPValidation(t *testing.T) {
	base := "models:\n  m:\n    base_url: u\n    model: x\nmcp:\n"
	if _, err := parseY(t, base+"  bad:\n    url: u\n    command: [c]\n", nil); err == nil {
		t.Fatal("command+url accepted")
	}
	if _, err := parseY(t, base+"  bad: {}\n", nil); err == nil {
		t.Fatal("neither command nor url accepted")
	}
	if _, err := parseY(t, base+"  bad:\n    url: u\n    allow: [a]\n    deny: [b]\n", nil); err == nil {
		t.Fatal("allow+deny accepted")
	}
	if _, err := parseY(t, base+"  BadName:\n    url: u\n", nil); err == nil {
		t.Fatal("bad server name accepted")
	}
}

func TestParseYAMLMediaNeedsModel(t *testing.T) {
	y := "models:\n  m:\n    base_url: u\n    model: x\nmedia:\n  tts: { voice: v }\n"
	if _, err := parseY(t, y, nil); err == nil {
		t.Fatal("tts without model accepted")
	}
}

func TestParseYAMLTTSModeDefault(t *testing.T) {
	y := "models:\n  m:\n    base_url: u\n    model: x\nmedia:\n  tts: { model: m }\n"
	c, err := parseY(t, y, nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.TTS().Mode != "inbound" {
		t.Fatalf("mode = %q", c.TTS().Mode)
	}
}

func TestParseYAMLPruneDefault(t *testing.T) {
	c, err := parseY(t, "models:\n  m:\n    base_url: u\n    model: x\n    compact_at: 100000\n", nil)
	if err != nil {
		t.Fatal(err)
	}
	if m, _ := c.Model("m"); m.PruneAt != 60000 {
		t.Fatalf("derived prune_at = %d, want 60000", m.PruneAt)
	}
	// Explicit 0 disables; explicit >= compact_at clamps to 0.
	c, _ = parseY(t, "models:\n  m:\n    base_url: u\n    model: x\n    compact_at: 100000\n    prune_at: 0\n", nil)
	if m, _ := c.Model("m"); m.PruneAt != 0 {
		t.Fatalf("explicit 0 prune_at = %d", m.PruneAt)
	}
	c, _ = parseY(t, "models:\n  m:\n    base_url: u\n    model: x\n    compact_at: 100000\n    prune_at: 200000\n", nil)
	if m, _ := c.Model("m"); m.PruneAt != 0 {
		t.Fatalf("clamped prune_at = %d", m.PruneAt)
	}
}

func TestParseYAMLPruneWithoutCompact(t *testing.T) {
	// An explicit prune_at with no compact_at would be silently dead at
	// runtime (both tiers key off compact_at), so it must fail the load.
	_, err := parseY(t, "models:\n  m:\n    base_url: u\n    model: x\n    prune_at: 60000\n", nil)
	if err == nil || !strings.Contains(err.Error(), "prune_at without compact_at") {
		t.Fatalf("err = %v", err)
	}
	// prune_at: 0 (explicitly disabled) stays fine without compact_at.
	if _, err := parseY(t, "models:\n  m:\n    base_url: u\n    model: x\n    prune_at: 0\n", nil); err != nil {
		t.Fatalf("explicit prune_at: 0 rejected: %v", err)
	}
}

func TestParseYAMLKeepRecentClamp(t *testing.T) {
	c, err := parseY(t, "models:\n  m:\n    base_url: u\n    model: x\n    compact_at: 800\n    keep_recent: 900\n", nil)
	if err != nil {
		t.Fatal(err)
	}
	if m, _ := c.Model("m"); m.KeepRecent != 400 {
		t.Fatalf("keep_recent = %d, want clamp to compact_at/2 = 400", m.KeepRecent)
	}
	c, _ = parseY(t, "models:\n  m:\n    base_url: u\n    model: x\n    compact_at: 800\n    keep_recent: 300\n", nil)
	if m, _ := c.Model("m"); m.KeepRecent != 300 {
		t.Fatalf("keep_recent below compact_at changed: %d", m.KeepRecent)
	}
}

func TestParseYAMLTTSModeInvalid(t *testing.T) {
	y := "models:\n  m:\n    base_url: u\n    model: x\nmedia:\n  tts: { model: m, mode: allways }\n"
	if _, err := parseY(t, y, nil); err == nil || !strings.Contains(err.Error(), "must be off, inbound, or always") {
		t.Fatalf("err = %v", err)
	}
}

func TestParseYAMLTTSFormatDefault(t *testing.T) {
	y := "models:\n  m:\n    base_url: u\n    model: x\nmedia:\n  tts: { model: m }\n"
	c, err := parseY(t, y, nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.TTS().Format != "opus" {
		t.Fatalf("format = %q, want opus default", c.TTS().Format)
	}
	y = "models:\n  m:\n    base_url: u\n    model: x\nmedia:\n  tts: { model: m, format: mp3 }\n"
	c, _ = parseY(t, y, nil)
	if c.TTS().Format != "mp3" {
		t.Fatalf("explicit format = %q", c.TTS().Format)
	}
}

func TestParseYAMLDescribePromptDefault(t *testing.T) {
	y := "models:\n  m:\n    base_url: u\n    model: x\nmedia:\n  describe: { model: m }\n"
	c, err := parseY(t, y, nil)
	if err != nil {
		t.Fatal(err)
	}
	if c.Describe().Prompt == "" {
		t.Fatal("describe prompt not defaulted")
	}
	y = "models:\n  m:\n    base_url: u\n    model: x\nmedia:\n  describe: { model: m, prompt: Caption it. }\n"
	c, _ = parseY(t, y, nil)
	if c.Describe().Prompt != "Caption it." {
		t.Fatalf("explicit prompt = %q", c.Describe().Prompt)
	}
}

func TestParseYAMLImagegenDefaultsAndValidation(t *testing.T) {
	y := "models:\n  m:\n    base_url: u\n    model: x\nmedia:\n  imagegen: { model: m }\n"
	c, err := parseY(t, y, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ig := c.Imagegen(); ig.API != "openai" || ig.Size != "1024x1024" {
		t.Fatalf("imagegen defaults = %+v", ig)
	}
	y = "models:\n  m:\n    base_url: u\n    model: x\nmedia:\n  imagegen: { model: m, api: openroutre }\n"
	if _, err := parseY(t, y, nil); err == nil || !strings.Contains(err.Error(), "must be openai or openrouter") {
		t.Fatalf("err = %v", err)
	}
}
