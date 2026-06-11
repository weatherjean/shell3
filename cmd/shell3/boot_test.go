//go:build unix

package main

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/agentsetup"
	"github.com/weatherjean/shell3/internal/luacfg"
)

func TestMergeEnvAddsMissingKeysOnly(t *testing.T) {
	existing := "FOO=bar\nMAIN_API_KEY=old\n"
	out := mergeEnv(existing, [][2]string{
		{"MAIN_API_KEY", "new"},
		{"BRAVE_API_KEY", "xyz"},
	})
	if !strings.Contains(out, "MAIN_API_KEY=old") {
		t.Errorf("must not overwrite existing key; got:\n%s", out)
	}
	if strings.Contains(out, "MAIN_API_KEY=new") {
		t.Errorf("must not append a duplicate for an existing key; got:\n%s", out)
	}
	if !strings.Contains(out, "BRAVE_API_KEY=xyz") {
		t.Errorf("must append missing key; got:\n%s", out)
	}
	if !strings.Contains(out, "FOO=bar") {
		t.Errorf("must preserve unrelated keys; got:\n%s", out)
	}
}

func TestMergeEnvFromEmpty(t *testing.T) {
	out := mergeEnv("", [][2]string{{"MAIN_API_KEY", "k"}, {"BRAVE_API_KEY", ""}})
	if !strings.Contains(out, "MAIN_API_KEY=k") || !strings.Contains(out, "BRAVE_API_KEY=") {
		t.Errorf("missing expected keys; got:\n%s", out)
	}
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("env file must end with newline; got:\n%q", out)
	}
}

func TestEnvKeyForName(t *testing.T) {
	if got := envKeyForName("main"); got != "MAIN_API_KEY" {
		t.Errorf("envKeyForName(main) = %q, want MAIN_API_KEY", got)
	}
	if got := envKeyForName("kimi-k2"); got != "KIMI_K2_API_KEY" {
		t.Errorf("envKeyForName(kimi-k2) = %q, want KIMI_K2_API_KEY", got)
	}
	// Degenerate handles must still yield a valid identifier.
	if got := envKeyForName("@@@"); got != "MAIN_API_KEY" {
		t.Errorf("envKeyForName(@@@) = %q, want MAIN_API_KEY (empty -> fallback)", got)
	}
	if got := envKeyForName(""); got != "MAIN_API_KEY" {
		t.Errorf("envKeyForName(empty) = %q, want MAIN_API_KEY", got)
	}
	if got := envKeyForName("123model"); got != "_123MODEL_API_KEY" {
		t.Errorf("envKeyForName(123model) = %q, want _123MODEL_API_KEY (leading digit)", got)
	}
}

// TestValueReadsVisibleLine covers the interactive read path used by every boot
// prompt — including the API key, which now echoes (read as a normal line)
// instead of being hidden. A long pasted token must come back intact.
func TestValueReadsVisibleLine(t *testing.T) {
	const pasted = "sk-proj-AbCdEf0123456789-very-long-pasted-key_value.0987654321"
	in := bufio.NewReader(strings.NewReader(pasted + "\n"))
	got, err := value("", "API key (blank if your proxy handles auth)", "", in, true, false)
	if err != nil {
		t.Fatalf("value: %v", err)
	}
	if got != pasted {
		t.Fatalf("value = %q, want %q", got, pasted)
	}
}

// TestValueBlankUsesDefault: an empty line returns the default (blank key is
// allowed — e.g. when a proxy handles auth).
func TestValueBlankUsesDefault(t *testing.T) {
	in := bufio.NewReader(strings.NewReader("\n"))
	got, err := value("", "API key", "", in, true, false)
	if err != nil {
		t.Fatalf("value: %v", err)
	}
	if got != "" {
		t.Fatalf("value = %q, want empty", got)
	}
}

// TestValueFlagWins: a provided flag short-circuits the prompt entirely.
func TestValueFlagWins(t *testing.T) {
	in := bufio.NewReader(strings.NewReader("ignored\n"))
	got, err := value("from-flag", "API key", "", in, true, false)
	if err != nil {
		t.Fatalf("value: %v", err)
	}
	if got != "from-flag" {
		t.Fatalf("value = %q, want from-flag", got)
	}
}

// TestBootEndToEnd drives the real `shell3 boot` flow against a temp HOME: it
// asserts the cold-start redirect before boot, runs runBoot with flags (no TTY),
// then verifies the written tree, .env (empty key + Brave placeholder, 0600),
// that the generated config actually loads through luacfg with the code/plan
// agents, the no-clobber guard, and that --force regenerates.
func TestBootEndToEnd(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}

	// Cold start: no config anywhere -> ResolveConfigPath must fail (the message
	// that points the user at `shell3 boot`). cwd (the package dir) has no
	// shell3.lua, and the temp HOME has none yet.
	if _, err := agentsetup.ResolveConfigPath("", cwd, home); err == nil {
		t.Fatal("expected no-config error before boot, got nil")
	}

	f := &bootFlags{url: "http://localhost:9999/v1", model: "test-model", name: "main", proxy: "echo proxy"}
	if err := runBoot(f); err != nil {
		t.Fatalf("runBoot: %v", err)
	}

	dir := filepath.Join(home, ".shell3")
	for _, p := range []string{
		"shell3.lua", "lib/tools.lua", "lib/guards.lua",
		"lib/skills/brainstorming.lua", ".env",
	} {
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			t.Errorf("missing %s: %v", p, err)
		}
	}

	// .env: empty model key (proxy handles auth) + Brave placeholder, mode 0600.
	envPath := filepath.Join(dir, ".env")
	env, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("read .env: %v", err)
	}
	if !strings.Contains(string(env), "MAIN_API_KEY=") {
		t.Errorf(".env missing MAIN_API_KEY line:\n%s", env)
	}
	if !strings.Contains(string(env), "BRAVE_API_KEY=") {
		t.Errorf(".env missing BRAVE_API_KEY placeholder:\n%s", env)
	}
	if fi, err := os.Stat(envPath); err != nil {
		t.Fatal(err)
	} else if fi.Mode().Perm() != 0o600 {
		t.Errorf(".env perms = %v, want 0600", fi.Mode().Perm())
	}

	// After boot, config resolution finds the home config.
	resolved, err := agentsetup.ResolveConfigPath("", cwd, home)
	if err != nil {
		t.Fatalf("ResolveConfigPath after boot: %v", err)
	}
	if resolved != filepath.Join(dir, "shell3.lua") {
		t.Errorf("resolved = %q, want home shell3.lua", resolved)
	}

	// The end-to-end payoff: the generated config loads with an empty api_key.
	c, err := luacfg.Load(resolved, dir)
	if err != nil {
		t.Fatalf("generated config failed to load: %v", err)
	}
	defer c.Close()
	agents := c.Agents()
	if len(agents) != 2 || agents[0].Name != "code" || agents[1].Name != "plan" {
		t.Errorf("agents = %v, want [code plan]", agentNames(agents))
	}

	// No-clobber: a second boot without --force refuses.
	if err := runBoot(f); err == nil {
		t.Error("second boot without --force should error (config exists)")
	}

	// --force regenerates with new values.
	f.force = true
	f.model = "changed-model"
	if err := runBoot(f); err != nil {
		t.Fatalf("force runBoot: %v", err)
	}
	cfg, _ := os.ReadFile(resolved)
	if !strings.Contains(string(cfg), `model          = "changed-model"`) {
		t.Errorf("--force did not regenerate the model; got:\n%s", cfg)
	}
}

func agentNames(agents []luacfg.Agent) []string {
	out := make([]string, len(agents))
	for i, a := range agents {
		out[i] = a.Name
	}
	return out
}

func TestBootTelegramEndToEnd(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	f := &bootFlags{
		url: "http://localhost:9999/v1", model: "test-model", name: "main",
		telegram: true, tgToken: "BOT:TOKEN", chatID: "424242",
		dashAddr: "127.0.0.1:8765", dashURL: "https://h.ts.net/",
	}
	if err := runBoot(f); err != nil {
		t.Fatalf("runBoot --telegram: %v", err)
	}

	dir := filepath.Join(home, ".shell3", "telegram")
	for _, p := range []string{"shell3.lua", "lib/tools.lua", "lib/guards.lua", ".env", "workdir"} {
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			t.Errorf("missing %s: %v", p, err)
		}
	}
	env, err := os.ReadFile(filepath.Join(dir, ".env"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(env), "TELEGRAM_BOT_TOKEN=BOT:TOKEN") {
		t.Errorf(".env missing bot token:\n%s", env)
	}
	if !strings.Contains(string(env), "MAIN_API_KEY=") {
		t.Errorf(".env missing model key:\n%s", env)
	}
	if fi, err := os.Stat(filepath.Join(dir, ".env")); err == nil && fi.Mode().Perm() != 0o600 {
		t.Errorf(".env perms = %v, want 0600", fi.Mode().Perm())
	}

	c, err := luacfg.Load(filepath.Join(dir, "shell3.lua"), dir)
	if err != nil {
		t.Fatalf("generated telegram config failed to load: %v", err)
	}
	defer c.Close()
	tg := c.Telegram()
	if tg.ChatID != "424242" || tg.Token != "BOT:TOKEN" || tg.Agent != "code" {
		t.Errorf("telegram = %+v, want chat_id=424242 token=BOT:TOKEN agent=code", tg)
	}
	if !tg.Dashboard.Enabled || tg.Dashboard.URL != "https://h.ts.net/" {
		t.Errorf("dashboard = %+v", tg.Dashboard)
	}

	cwd, _ := os.Getwd()
	resolved, err := agentsetup.ResolveTelegramConfigPath("", cwd, home)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != filepath.Join(dir, "shell3.lua") {
		t.Errorf("resolved = %q, want telegram shell3.lua", resolved)
	}

	if err := runBoot(&bootFlags{url: "http://x/v1", model: "m", name: "main"}); err != nil {
		t.Fatalf("generic boot: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".shell3", "shell3.lua")); err != nil {
		t.Errorf("generic boot did not write ~/.shell3/shell3.lua: %v", err)
	}
}
