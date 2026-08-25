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
	// The caller supplied a NEW non-empty value for an existing key: that key
	// must be reported so boot can tell the user their --key was not applied.
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

// TestMergeEnvKeptOnlyForNonEmptyIncoming: an existing key with a BLANK
// incoming value is normal re-boot behavior (nothing to apply), not worth a
// warning — only a discarded non-empty value is reported.
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

// TestCollectAnswersNonTTY covers the headless (flags-only) path: flags win,
// blanks take defaults, model is required, int flags are validated, and the
// vision flag flows through.
func TestCollectAnswersNonTTY(t *testing.T) {
	t.Run("defaults fill blanks", func(t *testing.T) {
		a, err := collectAnswers(&bootFlags{model: "m", vision: true}, false)
		if err != nil {
			t.Fatalf("collectAnswers: %v", err)
		}
		if a.url != defaultBaseURL || a.name != "main" {
			t.Errorf("defaults not applied: url=%q name=%q", a.url, a.name)
		}
		if a.ctxWindow != 128000 || a.compactAt != 102400 {
			t.Errorf("int defaults: ctx=%d compact=%d, want 128000/102400", a.ctxWindow, a.compactAt)
		}
		if !a.vision {
			t.Error("vision flag must flow through")
		}
	})

	// A chat id reaches the kit verbatim and is parsed as an int64 by the
	// front-end at startup, far from where it was typed — so boot rejects a
	// non-numeric one here, and lets a blank through (fill it in later).
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

// TestBootEndToEnd drives the real `shell3 boot` flow against a temp HOME: it
// asserts the cold-start redirect before boot, runs runBoot with flags (no TTY),
// then verifies the written tree, .env (empty key + Brave placeholder, 0600),
// that the generated config tree actually loads through internal/config,
// the no-clobber guard, and that --force regenerates.
func TestBootEndToEnd(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Cold start: no config anywhere -> ResolveConfigDir must fail (the message
	// that points the user at `shell3 boot`). The temp HOME has no config yet.
	if _, err := agentsetup.ResolveConfigDir("", home); err == nil {
		t.Fatal("expected no-config error before boot, got nil")
	}

	f := &bootFlags{url: "http://localhost:9999/v1", model: "test-model", name: "main", proxy: "echo proxy", vision: true}
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

	// .env: empty model key (proxy handles auth), mode 0600.
	envPath := filepath.Join(dir, ".env")
	env, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatalf("read .env: %v", err)
	}
	if !strings.Contains(string(env), "MAIN_API_KEY=") {
		t.Errorf(".env missing MAIN_API_KEY line:\n%s", env)
	}
	// The token key is always written, even blank: the config references it, so
	// a missing key is a load error rather than an empty-token startup refusal.
	if !strings.Contains(string(env), envTelegramToken+"=") {
		t.Errorf(".env missing %s line:\n%s", envTelegramToken, env)
	}
	if fi, err := os.Stat(envPath); err != nil {
		t.Fatal(err)
	} else if fi.Mode().Perm() != 0o600 {
		t.Errorf(".env perms = %v, want 0600", fi.Mode().Perm())
	}

	// After boot, config resolution finds the home config.
	resolved, err := agentsetup.ResolveConfigDir("", home)
	if err != nil {
		t.Fatalf("ResolveConfigDir after boot: %v", err)
	}
	if resolved != dir {
		t.Errorf("resolved = %q, want %q", resolved, dir)
	}

	// The end-to-end payoff: the generated config loads with an empty api_key.
	if _, err := config.Load(resolved); err != nil {
		t.Fatalf("generated config failed to load: %v", err)
	}
	// Vision=true opts the main agent into the media tool (read_media).
	kitBody, err := os.ReadFile(filepath.Join(dir, "shell3.sh"))
	if err != nil {
		t.Fatalf("read shell3.sh: %v", err)
	}
	if !strings.Contains(string(kitBody), "# use: [media]") ||
		strings.Contains(string(kitBody), "# # use: [media]") {
		t.Errorf("vision boot should opt into media; got:\n%s", kitBody)
	}
	if strings.Contains(string(kitBody), "\nmedia:") {
		t.Errorf("vision boot must not render a media: block; got:\n%s", kitBody)
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
	cfg, _ := os.ReadFile(filepath.Join(dir, "shell3.sh"))
	if !strings.Contains(string(cfg), `model: "changed-model"`) {
		t.Errorf("--force did not regenerate the model; got:\n%s", cfg)
	}
}

// A first boot that defers the bot token writes `TELEGRAM_TOKEN=` — a key
// that exists but holds nothing. A later `boot --force --tg-token …` must
// FILL that line, not treat it as an existing credential worth keeping:
// keeping it discards the typed token and then reports it as kept, leaving
// the user with a config that refuses to start and a note saying otherwise.
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

// The inverse still holds: a key with a REAL value is never overwritten, and
// the discard is reported so the user knows their new value was not applied.
func TestMergeEnvStillKeepsNonEmptyExisting(t *testing.T) {
	out, kept := mergeEnv("TELEGRAM_TOKEN=999:OLD\n", [][2]string{{envTelegramToken, "123:NEW"}})
	if !strings.Contains(out, "TELEGRAM_TOKEN=999:OLD") || strings.Contains(out, "123:NEW") {
		t.Errorf("an existing token must survive; got:\n%s", out)
	}
	if len(kept) != 1 || kept[0] != envTelegramToken {
		t.Errorf("kept = %v, want [%s]", kept, envTelegramToken)
	}
}

// A blank token writes the fill-it-in-later comment; a supplied one must not
// (a comment telling the user to fill in a line that is already filled).
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
