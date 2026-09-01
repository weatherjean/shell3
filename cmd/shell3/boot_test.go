//go:build unix

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/agentsetup"
	"github.com/weatherjean/shell3/internal/config"
)

func TestMergeEnvAddsMissingKeysOnly(t *testing.T) {
	existing := "FOO=bar\nMAIN_API_KEY=old\n"
	out, kept := mergeEnv(existing, [][2]string{
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
	if len(kept) != 1 || kept[0] != "MAIN_API_KEY" {
		t.Errorf("kept = %v, want [MAIN_API_KEY]", kept)
	}
}

func TestMergeEnvFromEmpty(t *testing.T) {
	out, kept := mergeEnv("", [][2]string{{"MAIN_API_KEY", "k"}, {"BRAVE_API_KEY", ""}})
	if !strings.Contains(out, "MAIN_API_KEY=k") || !strings.Contains(out, "BRAVE_API_KEY=") {
		t.Errorf("missing expected keys; got:\n%s", out)
	}
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("env file must end with newline; got:\n%q", out)
	}
	if len(kept) != 0 {
		t.Errorf("nothing pre-existing, kept must be empty; got %v", kept)
	}
}

func TestMergeEnvKeptOnlyForNonEmptyIncoming(t *testing.T) {
	_, kept := mergeEnv("MAIN_API_KEY=old\nGROQ_API_KEY=t\n", [][2]string{
		{"MAIN_API_KEY", ""},
		{"GROQ_API_KEY", "freshly-typed"},
	})
	if len(kept) != 1 || kept[0] != "GROQ_API_KEY" {
		t.Errorf("kept = %v, want [GROQ_API_KEY] (blank incoming dropped)", kept)
	}
}

func TestEnvKeyForName(t *testing.T) {
	if got := envKeyForName("main"); got != "MAIN_API_KEY" {
		t.Errorf("envKeyForName(main) = %q, want MAIN_API_KEY", got)
	}
	if got := envKeyForName("kimi-k2"); got != "KIMI_K2_API_KEY" {
		t.Errorf("envKeyForName(kimi-k2) = %q, want KIMI_K2_API_KEY", got)
	}
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

func TestCollectAnswersNonTTY(t *testing.T) {
	t.Run("defaults fill blanks", func(t *testing.T) {
		a, err := collectAnswers(&bootFlags{model: "m"}, false)
		if err != nil {
			t.Fatalf("collectAnswers: %v", err)
		}
		if a.url != defaultBaseURL || a.name != "main" {
			t.Errorf("defaults not applied: url=%q name=%q", a.url, a.name)
		}
		if a.ctxWindow != 128000 || a.compactAt != 102400 {
			t.Errorf("int defaults: ctx=%d compact=%d, want 128000/102400", a.ctxWindow, a.compactAt)
		}
	})

	t.Run("chat id validated", func(t *testing.T) {
		if _, err := collectAnswers(&bootFlags{model: "m", tgChatID: "@me"}, false); err == nil {
			t.Fatal("expected a non-numeric chat id to be rejected")
		}
		a, err := collectAnswers(&bootFlags{model: "m", tgChatID: " 123456789 "}, false)
		if err != nil {
			t.Fatalf("collectAnswers: %v", err)
		}
		if a.tgChatID != "123456789" {
			t.Errorf("chat id = %q, want it trimmed to 123456789", a.tgChatID)
		}
		if _, err := collectAnswers(&bootFlags{model: "m"}, false); err != nil {
			t.Fatalf("a blank chat id must be allowed: %v", err)
		}
	})

	t.Run("model required", func(t *testing.T) {
		if _, err := collectAnswers(&bootFlags{}, false); err == nil {
			t.Fatal("expected --model required error")
		}
	})

	t.Run("bad int flag rejected", func(t *testing.T) {
		if _, err := collectAnswers(&bootFlags{model: "m", contextWindow: "lots"}, false); err == nil {
			t.Fatal("expected positive-integer error")
		}
	})

	t.Run("compact-at defaults to 80% of explicit window", func(t *testing.T) {
		a, err := collectAnswers(&bootFlags{model: "m", contextWindow: "200000"}, false)
		if err != nil {
			t.Fatalf("collectAnswers: %v", err)
		}
		if a.ctxWindow != 200000 || a.compactAt != 160000 {
			t.Errorf("ctx=%d compact=%d, want 200000/160000", a.ctxWindow, a.compactAt)
		}
	})
}

func TestBootEndToEnd(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	if _, err := agentsetup.ResolveConfigDir("", home); err == nil {
		t.Fatal("expected no-config error before boot, got nil")
	}

	f := &bootFlags{url: "http://localhost:9999/v1", model: "test-model", name: "main", proxy: "echo proxy"}
	if err := runBoot(f); err != nil {
		t.Fatalf("runBoot: %v", err)
	}

	dir := filepath.Join(home, ".shell3")
	for _, p := range []string{
		"shell3.sh",
		"skills/planning.md", "skills/scripting.md", ".env",
	} {
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			t.Errorf("missing %s: %v", p, err)
		}
	}

	envPath := filepath.Join(dir, ".env")
	env, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("read .env: %v", err)
	}
	if !strings.Contains(string(env), "MAIN_API_KEY=") {
		t.Errorf(".env missing MAIN_API_KEY line:\n%s", env)
	}
	if !strings.Contains(string(env), envTelegramToken+"=") {
		t.Errorf(".env missing %s line:\n%s", envTelegramToken, env)
	}
	if fi, err := os.Stat(envPath); err != nil {
		t.Fatal(err)
	} else if fi.Mode().Perm() != 0o600 {
		t.Errorf(".env perms = %v, want 0600", fi.Mode().Perm())
	}

	resolved, err := agentsetup.ResolveConfigDir("", home)
	if err != nil {
		t.Fatalf("ResolveConfigDir after boot: %v", err)
	}
	if resolved != dir {
		t.Errorf("resolved = %q, want %q", resolved, dir)
	}

	if _, err := config.Load(resolved); err != nil {
		t.Fatalf("generated config failed to load: %v", err)
	}
	if err := runBoot(f); err == nil {
		t.Error("second boot without --force should error (config exists)")
	}

	f.force = true
	f.model = "changed-model"
	if err := runBoot(f); err != nil {
		t.Fatalf("force runBoot: %v", err)
	}
	cfg, _ := os.ReadFile(filepath.Join(dir, "shell3.sh"))
	if !strings.Contains(string(cfg), `model: "changed-model"`) {
		t.Errorf("--force did not regenerate the model; got:\n%s", cfg)
	}
}

func TestMergeEnvFillsBlankValueInsteadOfKeepingIt(t *testing.T) {
	existing := "MAIN_API_KEY=\n# Telegram bot token from @BotFather — fill in before `shell3 telegram`.\nTELEGRAM_TOKEN=\n"
	out, kept := mergeEnv(existing, [][2]string{
		{"MAIN_API_KEY", "sk-new"},
		{envTelegramToken, "123:ABC"},
	})
	if !strings.Contains(out, "TELEGRAM_TOKEN=123:ABC") {
		t.Errorf("a freshly typed token must fill the blank line; got:\n%s", out)
	}
	if !strings.Contains(out, "MAIN_API_KEY=sk-new") {
		t.Errorf("a freshly typed key must fill the blank line; got:\n%s", out)
	}
	if strings.Count(out, "TELEGRAM_TOKEN=") != 1 {
		t.Errorf("filling must happen in place, not append a duplicate key; got:\n%s", out)
	}
	if len(kept) != 0 {
		t.Errorf("kept = %v, want empty — nothing was discarded", kept)
	}
}

func TestMergeEnvStillKeepsNonEmptyExisting(t *testing.T) {
	out, kept := mergeEnv("TELEGRAM_TOKEN=999:OLD\n", [][2]string{{envTelegramToken, "123:NEW"}})
	if !strings.Contains(out, "TELEGRAM_TOKEN=999:OLD") || strings.Contains(out, "123:NEW") {
		t.Errorf("an existing token must survive; got:\n%s", out)
	}
	if len(kept) != 1 || kept[0] != envTelegramToken {
		t.Errorf("kept = %v, want [%s]", kept, envTelegramToken)
	}
}

func TestMergeEnvTokenCommentOnlyWhenBlank(t *testing.T) {
	blank, _ := mergeEnv("", [][2]string{{envTelegramToken, ""}})
	if !strings.Contains(blank, "BotFather") {
		t.Errorf("a deferred token should carry the fill-it-in hint; got:\n%s", blank)
	}
	supplied, _ := mergeEnv("", [][2]string{{envTelegramToken, "123:ABC"}})
	if strings.Contains(supplied, "BotFather") {
		t.Errorf("a supplied token needs no fill-it-in hint; got:\n%s", supplied)
	}
}
