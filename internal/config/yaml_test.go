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
	if _, err := parseY(t, base+"  bad:\n    url: u\n    timeout: -1\n", nil); err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("negative timeout: want a named load error, got %v", err)
	}
}

func TestParseYAMLRejectsNegativeControlValues(t *testing.T) {
	for _, tc := range []struct {
		name string
		yaml string
		key  string
	}{
		{"context window", "models:\n  m: {base_url: u, model: x, context_window: -1}\n", "context_window"},
		{"compact at", "models:\n  m: {base_url: u, model: x, compact_at: -1}\n", "compact_at"},
		{"keep recent", "models:\n  m: {base_url: u, model: x, keep_recent: -1}\n", "keep_recent"},
		{"prune at", "models:\n  m: {base_url: u, model: x, prune_at: -1}\n", "prune_at"},
		{"max tokens", "models:\n  m: {base_url: u, model: x, max_tokens: -1}\n", "max_tokens"},
		{"background cap", "models:\n  m: {base_url: u, model: x}\nbackground: {max_concurrent: -1}\n", "max_concurrent"},
		{"telegram cap", "models:\n  m: {base_url: u, model: x}\ntelegram: {max_concurrent_turns: -1}\n", "max_concurrent_turns"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseY(t, tc.yaml, nil)
			if err == nil || !strings.Contains(err.Error(), tc.key) {
				t.Fatalf("want a load error naming %s, got %v", tc.key, err)
			}
		})
	}
}

func TestParseYAMLCompactAtCannotExceedContextWindow(t *testing.T) {
	_, err := parseY(t, "models:\n  m: {base_url: u, model: x, context_window: 100, compact_at: 101}\n", nil)
	if err == nil || !strings.Contains(err.Error(), "exceeds context_window") {
		t.Fatalf("want a threshold ordering error, got %v", err)
	}
}

func TestParseYAMLDuplicateTelegramChatFails(t *testing.T) {
	y := "models:\n  m: {base_url: u, model: x}\ntelegram:\n  chats:\n    - {id: \"123\"}\n    - {id: \"123\", use_description: false}\n"
	_, err := parseY(t, y, nil)
	if err == nil || !strings.Contains(err.Error(), "declared more than once") {
		t.Fatalf("want a duplicate chat error, got %v", err)
	}
}

// TestParseYAMLMediaBlockIsUnknownKey pins the subtraction: the media: block
// (imagegen/describe/tts/stt, all now removed) is a removed key entirely, so
// declaring any sub-block under it is a strict-decode load error like any
// other unknown field — never silently ignored, never a deprecation warning.
func TestParseYAMLMediaBlockIsUnknownKey(t *testing.T) {
	for _, y := range []string{
		"models:\n  m:\n    base_url: u\n    model: x\nmedia:\n  tts: { model: m }\n",
		"models:\n  m:\n    base_url: u\n    model: x\nmedia:\n  stt: { model: m }\n",
	} {
		if _, err := parseY(t, y, nil); err == nil {
			t.Fatalf("media: block accepted, want unknown-key load error: %q", y)
		}
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
	_, err := parseY(t, "models:\n  m:\n    base_url: u\n    model: x\n    prune_at: 60000\n", nil)
	if err == nil || !strings.Contains(err.Error(), "prune_at without compact_at") {
		t.Fatalf("err = %v", err)
	}
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

// A strict-decode failure must name the wiring's own blocks, not the Go types
// behind them.
func TestParseYAMLUnknownKeyNamesConfigNotGoTypes(t *testing.T) {
	for _, tc := range []struct {
		name string
		yaml string
		want string
	}{
		{"top level", "web:\n  password: x\n", "the shell3: block"},
		{"telegram block", "models:\n  m:\n    base_url: u\n    model: x\ntelegram:\n  dashboard: {}\n", "telegram:"},
		{"mcp sub-block", "models:\n  m:\n    base_url: u\n    model: x\nmcp:\n  srv:\n    bogus: 1\n", "an mcp: server"},
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
// the generic "the shell3: block" label silently. Walk yamlFile's type graph and
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

func TestParseYAMLReviewKeys(t *testing.T) {
	c, err := parseY(t, fullYAML, fullSecrets)
	if err != nil {
		t.Fatal(err)
	}
	if c.ReviewModel != "" || c.ReviewPolicy != "" {
		t.Fatalf("defaults = %q/%q, want empty", c.ReviewModel, c.ReviewPolicy)
	}
	c, err = parseY(t, fullYAML+"review_model: aux\nreview_policy: always DENY /etc writes\n", fullSecrets)
	if err != nil {
		t.Fatal(err)
	}
	if c.ReviewModel != "aux" || c.ReviewPolicy != "always DENY /etc writes" {
		t.Fatalf("got %q/%q", c.ReviewModel, c.ReviewPolicy)
	}
	if _, err := parseY(t, fullYAML+"review_model: nope\n", fullSecrets); err == nil ||
		!strings.Contains(err.Error(), "review_model") {
		t.Fatalf("want error naming review_model, got %v", err)
	}
}

func TestParseYAMLDashPort(t *testing.T) {
	c, err := parseY(t, fullYAML, fullSecrets)
	if err != nil {
		t.Fatal(err)
	}
	if c.DashPort != DefaultDashPort {
		t.Fatalf("DashPort default = %d, want %d", c.DashPort, DefaultDashPort)
	}
	c, err = parseY(t, fullYAML+"dash_port: 0\n", fullSecrets)
	if err != nil {
		t.Fatal(err)
	}
	if c.DashPort != 0 {
		t.Fatalf("DashPort = %d, want 0 (disabled)", c.DashPort)
	}
	c, err = parseY(t, fullYAML+"dash_port: 8080\n", fullSecrets)
	if err != nil {
		t.Fatal(err)
	}
	if c.DashPort != 8080 {
		t.Fatalf("DashPort = %d, want 8080", c.DashPort)
	}
	for _, bad := range []string{"dash_port: -1\n", "dash_port: 70000\n"} {
		if _, err := parseY(t, fullYAML+bad, fullSecrets); err == nil ||
			!strings.Contains(err.Error(), "dash_port") {
			t.Fatalf("%q: want error naming dash_port, got %v", bad, err)
		}
	}
}
