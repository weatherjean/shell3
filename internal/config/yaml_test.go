package config

import (
	"reflect"
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
  workdir: /tmp/agent
  chat_id: "123456789"
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
background:
  max_concurrent: 4
`

var fullSecrets = map[string]string{
	"DEEPSEEK_API_KEY": "sk-1",
	"LINEAR_KEY":       "lk", "GH": "gh",
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
	if tg := c.Telegram(); tg.ChatID != "123456789" || tg.WorkDir != "/tmp/agent" {
		t.Fatalf("telegram = %+v", tg)
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
	if c.STT() == nil || c.STT().ModelRef != "aux" {
		t.Fatal("media blocks missing")
	}
	if c.BackgroundMaxConcurrent != 4 {
		t.Fatalf("background = %d", c.BackgroundMaxConcurrent)
	}
}

func TestParseYAMLMediaKeepDaysDefault(t *testing.T) {
	c, err := parseY(t, fullYAML, fullSecrets)
	if err != nil {
		t.Fatal(err)
	}
	if c.MediaKeepDays != 0 {
		t.Fatalf("MediaKeepDays default = %d, want 0 (keep forever)", c.MediaKeepDays)
	}
}

func TestParseYAMLKeepDaysRejectsNegative(t *testing.T) {
	for _, key := range []string{"runs_keep_days", "media_keep_days"} {
		_, err := parseY(t, fullYAML+key+": -1\n", fullSecrets)
		if err == nil {
			t.Fatalf("%s: -1: want load error, got nil", key)
		}
		if !strings.Contains(err.Error(), key) {
			t.Fatalf("%s: -1: error %q doesn't name the key", key, err)
		}
	}
}

func TestParseYAMLKeepDaysRejectsOverflowRisk(t *testing.T) {
	// 213504 days is the reviewer's repro: time.Duration(days)*24*time.Hour
	// overflows int64 nanoseconds and wraps around to a small POSITIVE
	// duration (~25 minutes), silently inverting "keep basically forever"
	// into "delete almost everything". Must be rejected at load, well below
	// where the wraparound happens.
	for _, key := range []string{"runs_keep_days", "media_keep_days"} {
		_, err := parseY(t, fullYAML+key+": 213504\n", fullSecrets)
		if err == nil {
			t.Fatalf("%s: 213504: want load error, got nil", key)
		}
		if !strings.Contains(err.Error(), key) {
			t.Fatalf("%s: 213504: error %q doesn't name the key", key, err)
		}
	}
}

func TestParseYAMLKeepDaysAllowsReasonableValues(t *testing.T) {
	c, err := parseY(t, fullYAML+"runs_keep_days: 30\nmedia_keep_days: 7\n", fullSecrets)
	if err != nil {
		t.Fatal(err)
	}
	if c.RunsKeepDays != 30 || c.MediaKeepDays != 7 {
		t.Fatalf("RunsKeepDays=%d MediaKeepDays=%d, want 30/7", c.RunsKeepDays, c.MediaKeepDays)
	}
	// 0 (keep forever) must still be allowed explicitly.
	c, err = parseY(t, fullYAML+"runs_keep_days: 0\n", fullSecrets)
	if err != nil {
		t.Fatal(err)
	}
	if c.RunsKeepDays != 0 {
		t.Fatalf("RunsKeepDays = %d, want 0", c.RunsKeepDays)
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
	if _, err := parseY(t, "telegram: { chat_id: \"1\" }\n", nil); err == nil || !strings.Contains(err.Error(), "no models") {
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

// TestParseYAMLMediaTTSIsUnknownKey pins the subtraction: media.tts is a
// removed key, so declaring it is a strict-decode load error like any other
// unknown field — never silently ignored, never a deprecation warning.
func TestParseYAMLMediaTTSIsUnknownKey(t *testing.T) {
	y := "models:\n  m:\n    base_url: u\n    model: x\nmedia:\n  tts: { model: m }\n"
	if _, err := parseY(t, y, nil); err == nil {
		t.Fatal("media.tts accepted, want unknown-key load error")
	}
}

// TestParseYAMLMediaSTTNeedsModel pins the surviving half of the pattern the
// deleted media.tts-without-model test used to cover: a media.stt block
// still requires a model.
func TestParseYAMLMediaSTTNeedsModel(t *testing.T) {
	y := "models:\n  m:\n    base_url: u\n    model: x\nmedia:\n  stt: { echo: true }\n"
	if _, err := parseY(t, y, nil); err == nil || !strings.Contains(err.Error(), "media.stt needs a model") {
		t.Fatalf("err = %v, want \"media.stt needs a model\"", err)
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

// A strict-decode failure is the first thing a 0.4.x upgrader with a `web:`
// block meets, so it must name shell3.yaml's own blocks — not the Go types
// behind them.
func TestParseYAMLUnknownKeyNamesConfigNotGoTypes(t *testing.T) {
	for _, tc := range []struct {
		name string
		yaml string
		want string
	}{
		{"top level", "web:\n  password: x\n", "shell3.yaml"},
		{"telegram block", "models:\n  m:\n    base_url: u\n    model: x\ntelegram:\n  dashboard: {}\n", "telegram:"},
		{"media sub-block", "models:\n  m:\n    base_url: u\n    model: x\nmedia:\n  stt:\n    bogus: 1\n", "media.stt"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseY(t, tc.yaml, nil)
			if err == nil {
				t.Fatal("expected an unknown-key error")
			}
			if strings.Contains(err.Error(), "config.yaml") {
				t.Errorf("error leaks an internal Go type: %v", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error should name %q, got %v", tc.want, err)
			}
		})
	}
}

// yamlTypeNames is a hand-maintained shadow of the yaml* wire structs; a new
// block added without a map entry would degrade its strict-decode errors to
// the generic "shell3.yaml" label silently. Walk yamlFile's type graph and
// assert coverage, so the drift is a test failure instead.
func TestYAMLTypeNamesCoverEveryWireStruct(t *testing.T) {
	seen := map[string]bool{}
	var walk func(rt reflect.Type)
	walk = func(rt reflect.Type) {
		for rt.Kind() == reflect.Pointer || rt.Kind() == reflect.Map || rt.Kind() == reflect.Slice {
			rt = rt.Elem()
		}
		if rt.Kind() != reflect.Struct || !strings.HasPrefix(rt.Name(), "yaml") || seen[rt.Name()] {
			return
		}
		seen[rt.Name()] = true
		for i := 0; i < rt.NumField(); i++ {
			walk(rt.Field(i).Type)
		}
	}
	walk(reflect.TypeOf(yamlFile{}))
	for name := range seen {
		if _, ok := yamlTypeNames[name]; !ok {
			t.Errorf("yamlTypeNames is missing %q — its strict-decode errors will read as Go, not config", name)
		}
	}
	for name := range yamlTypeNames {
		if !seen[name] {
			t.Errorf("yamlTypeNames names %q, which is no longer a wire struct", name)
		}
	}
}
