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
		"shell3.yaml", "agent.md", "agents/assistant.md",
		"hooks/tool-call.sh",
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
	c, err := config.Load(resolved)
	if err != nil {
		t.Fatalf("generated config failed to load: %v", err)
	}
	defer c.Close()
	if c.FirstAgent().Name != "agent" {
		t.Errorf("agent = %q, want %q", c.FirstAgent().Name, "agent")
	}

	// vision=true wires describe to the main model in the rendered config.
	if c.Describe() == nil || c.Describe().ModelRef != "main" {
		t.Errorf("vision boot should wire media.describe to the main model, got %+v", c.Describe())
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
	cfg, _ := os.ReadFile(filepath.Join(dir, "shell3.yaml"))
	if !strings.Contains(string(cfg), `model: "changed-model"`) {
		t.Errorf("--force did not regenerate the model; got:\n%s", cfg)
	}
}

// boot is where a fresh install gets its password, and 16 characters is the
// floor because this password guards a shell.
func TestValidateWebPassword(t *testing.T) {
	if err := validateWebPassword(""); err == nil {
		t.Error("an empty password was accepted")
	}
	if err := validateWebPassword("short"); err == nil {
		t.Error("a 5-character password was accepted")
	}
	if err := validateWebPassword(strings.Repeat("x", minPasswordLength)); err != nil {
		t.Errorf("a %d-character password was refused: %v", minPasswordLength, err)
	}
}

// The generated option has to clear the bar it sets, and not repeat itself.
func TestGenerateWebPassword(t *testing.T) {
	first, err := generateWebPassword()
	if err != nil {
		t.Fatal(err)
	}
	if err := validateWebPassword(first); err != nil {
		t.Errorf("the generated password fails our own validator: %v", err)
	}
	second, err := generateWebPassword()
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Error("two generated passwords are identical")
	}
}

// Enrolment gives the authenticator app a secret, and the operator a URI to
// scan. The URI has to name shell3 so the code is identifiable in the app.
func TestNewTOTPEnrolment(t *testing.T) {
	secret, uri, err := newTOTPEnrolment("agent")
	if err != nil {
		t.Fatal(err)
	}
	if secret == "" {
		t.Error("no secret generated")
	}
	if !strings.HasPrefix(uri, "otpauth://totp/") {
		t.Errorf("uri = %q, want an otpauth:// URI", uri)
	}
	if !strings.Contains(uri, "shell3") {
		t.Errorf("uri = %q, want shell3 as the issuer", uri)
	}
}

// The two secrets have to land in .env under the names shell3.yaml references,
// or the config resolves to nothing and serve refuses to start.
func TestWebEnvPairsCarryBothSecrets(t *testing.T) {
	pairs := webEnvPairs("a-long-enough-password", "TOTPSECRET")

	got := map[string]string{}
	for _, p := range pairs {
		got[p[0]] = p[1]
	}
	if got[envWebPassword] != "a-long-enough-password" {
		t.Errorf("%s = %q", envWebPassword, got[envWebPassword])
	}
	if got[envWebTOTP] != "TOTPSECRET" {
		t.Errorf("%s = %q", envWebTOTP, got[envWebTOTP])
	}
}

// No second factor means no key: an empty SHELL3_WEB_TOTP_SECRET= line in .env
// would read as "configured" to anyone looking at the file.
func TestWebEnvPairsOmitTOTPWhenNotEnrolled(t *testing.T) {
	for _, p := range webEnvPairs("a-long-enough-password", "") {
		if p[0] == envWebTOTP {
			t.Errorf("%s written with no enrolment", envWebTOTP)
		}
	}
}
