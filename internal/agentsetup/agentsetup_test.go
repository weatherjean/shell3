package agentsetup_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/agentsetup"
	"github.com/weatherjean/shell3/internal/luacfg"
)

func TestBuild_MissingConfig_Errors(t *testing.T) {
	tmp := t.TempDir()
	_, _, err := agentsetup.Build(agentsetup.Options{
		ConfigPath: filepath.Join(tmp, "shell3.lua"),
		CWD:        tmp,
		HomeDir:    tmp,
	})
	if err == nil {
		t.Fatal("expected error for missing config, got nil")
	}
}

func TestBuild_LoadsConfig(t *testing.T) {
	tmp := t.TempDir()
	home := t.TempDir()
	writeMinimalConfig(t, tmp)

	cfg, cleanup, err := agentsetup.Build(agentsetup.Options{
		ConfigPath: filepath.Join(tmp, "shell3.lua"),
		CWD:        tmp,
		HomeDir:    home,
		Headless:   true,
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer cleanup()

	if cfg.LLM == nil {
		t.Error("cfg.LLM is nil")
	}
	if cfg.Personality.SystemPrompt == "" {
		t.Error("cfg.Personality.SystemPrompt is empty")
	}
	if cfg.WorkDir != tmp {
		t.Errorf("WorkDir = %q, want %q", cfg.WorkDir, tmp)
	}
}

// writeTwoAgentConfig writes a config with two agents ("first", "second")
// sharing one model, for exercising initial-agent selection.
func writeTwoAgentConfig(t *testing.T, dir string) {
	t.Helper()
	lua := `
shell3.model("main", {
  base_url = "https://example.test/v1",
  api_key = shell3.env.secret("TEST_KEY"),
  model = "test-model",
  context_window = 1000,
})
shell3.agent({ name = "first",  model = "main", prompt = "you are first",  tools = {} })
shell3.agent({ name = "second", model = "main", prompt = "you are second", tools = {} })
`
	if err := os.WriteFile(filepath.Join(dir, "shell3.lua"), []byte(lua), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("TEST_KEY=sk-test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestBuild_Agent_DefaultsToFirst(t *testing.T) {
	tmp := t.TempDir()
	writeTwoAgentConfig(t, tmp)

	cfg, cleanup, err := agentsetup.Build(agentsetup.Options{
		ConfigPath: filepath.Join(tmp, "shell3.lua"),
		CWD:        tmp,
		HomeDir:    t.TempDir(),
		Headless:   true,
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer cleanup()
	if cfg.ModeLabel != "first" {
		t.Errorf("default active agent = %q, want %q", cfg.ModeLabel, "first")
	}
}

func TestBuild_Agent_SelectsNamed(t *testing.T) {
	tmp := t.TempDir()
	writeTwoAgentConfig(t, tmp)

	cfg, cleanup, err := agentsetup.Build(agentsetup.Options{
		ConfigPath: filepath.Join(tmp, "shell3.lua"),
		CWD:        tmp,
		HomeDir:    t.TempDir(),
		Headless:   true,
		Agent:      "second",
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer cleanup()
	if cfg.ModeLabel != "second" {
		t.Errorf("active agent = %q, want %q", cfg.ModeLabel, "second")
	}
	if cfg.Personality.SystemPrompt != "you are second" {
		t.Errorf("system prompt = %q, want the second agent's", cfg.Personality.SystemPrompt)
	}
}

func TestBuild_Agent_UnknownErrors(t *testing.T) {
	tmp := t.TempDir()
	writeTwoAgentConfig(t, tmp)

	_, _, err := agentsetup.Build(agentsetup.Options{
		ConfigPath: filepath.Join(tmp, "shell3.lua"),
		CWD:        tmp,
		HomeDir:    t.TempDir(),
		Headless:   true,
		Agent:      "nope",
	})
	if err == nil {
		t.Fatal("expected error for unknown agent, got nil")
	}
	if !strings.Contains(err.Error(), "nope") {
		t.Errorf("error should name the unknown agent, got: %v", err)
	}
}

func TestBuild_RunProxy_SpawnsOnActivation(t *testing.T) {
	tmp := t.TempDir()
	marker := filepath.Join(tmp, "proxy-started")
	lua := `
shell3.model("main", {
  base_url = "http://localhost:8787/v1",
  api_key = shell3.env.secret("TEST_KEY"),
  model = "test-model",
  run_proxy = "touch ` + marker + `",
})
shell3.agent({ name = "tester", model = "main", prompt = "hi", tools = {} })
`
	if err := os.WriteFile(filepath.Join(tmp, "shell3.lua"), []byte(lua), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, ".env"), []byte("TEST_KEY=sk-test\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, cleanup, err := agentsetup.Build(agentsetup.Options{
		ConfigPath: filepath.Join(tmp, "shell3.lua"),
		CWD:        tmp,
		HomeDir:    t.TempDir(),
		Headless:   true,
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer cleanup()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(marker); err == nil {
			return // proxy command ran
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("run_proxy command was not spawned on model activation")
}

// twoAgentOptions writes the two-agent config ("first", "second") via
// writeTwoAgentConfig and returns Options pointing at it, with a fresh isolated
// HomeDir so no real ~/.shell3 is touched.
func twoAgentOptions(t *testing.T) agentsetup.Options {
	t.Helper()
	tmp := t.TempDir()
	writeTwoAgentConfig(t, tmp)
	return agentsetup.Options{
		ConfigPath: filepath.Join(tmp, "shell3.lua"),
		CWD:        tmp,
		HomeDir:    t.TempDir(),
		Headless:   true,
	}
}

// TestSessionConfigs_IndependentAgentSwitch pins the phase-1 invariant: two
// configs derived from one Parts hold independent agent state — switching one
// never changes the other (the old global activeIdx is gone).
func TestSessionConfigs_IndependentAgentSwitch(t *testing.T) {
	parts, cleanup, err := agentsetup.BuildParts(twoAgentOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	a, err := parts.SessionConfig(agentsetup.SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := parts.SessionConfig(agentsetup.SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}

	rt, err := b.SwitchAgent("second")
	if err != nil {
		t.Fatal(err)
	}
	b.ApplyActiveAgent(rt)

	if a.ModeLabel != "first" {
		t.Fatalf("config A's agent changed to %q when B switched", a.ModeLabel)
	}
	if b.ModeLabel != "second" {
		t.Fatalf("config B should be second, got %q", b.ModeLabel)
	}
	// RefreshPrompt follows each session's own agent.
	if a.RefreshPrompt() == b.RefreshPrompt() {
		t.Fatal("RefreshPrompt should render different prompts for different active agents")
	}
}

// writeMinimalConfig writes a shell3.lua + .env that Build can load: one model
// referencing an env-injected key, and one agent selecting it. The Lua surface
// matches internal/luacfg's loader: shell3.model("name", {base_url, api_key,
// model, ...}) and shell3.agent({name, model, prompt, tools}). The .env sits
// beside the lua so shell3.env.secret resolves (Load reads .env from the config
// file's directory).
func writeMinimalConfig(t *testing.T, dir string) {
	t.Helper()
	lua := `
shell3.model("main", {
  base_url = "https://example.test/v1",
  api_key = shell3.env.secret("TEST_KEY"),
  model = "test-model",
  context_window = 1000,
})
shell3.agent({ name = "tester", model = "main", prompt = "you are a tester", tools = {} })
`
	if err := os.WriteFile(filepath.Join(dir, "shell3.lua"), []byte(lua), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("TEST_KEY=sk-test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestBuild_MalformedConfig_Errors characterizes the post-log-open error path:
// a present but syntactically invalid shell3.lua resolves (so the log opens),
// then luacfg.Load fails — Build must surface the error.
func TestBuild_MalformedConfig_Errors(t *testing.T) {
	tmp := t.TempDir()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "shell3.lua"), []byte("this is ((( not valid lua\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, ".env"), []byte("TEST_KEY=sk-test\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, _, err := agentsetup.Build(agentsetup.Options{
		ConfigPath: filepath.Join(tmp, "shell3.lua"),
		CWD:        tmp,
		HomeDir:    home,
		Headless:   true,
	})
	if err == nil {
		t.Fatal("expected error for malformed config, got nil")
	}
}

// writeConfigWithHistory writes a shell3.lua whose agent enables the history
// tool, so Build opens the store (Gates.History). Mirrors writeMinimalConfig
// but flips tools = { history = true }.
func writeConfigWithHistory(t *testing.T, dir string) {
	t.Helper()
	lua := `
shell3.model("main", {
  base_url = "https://example.test/v1",
  api_key = shell3.env.secret("TEST_KEY"),
  model = "test-model",
  context_window = 1000,
})
shell3.agent({ name = "tester", model = "main", prompt = "you are a tester", tools = { history = true } })
`
	if err := os.WriteFile(filepath.Join(dir, "shell3.lua"), []byte(lua), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("TEST_KEY=sk-test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestBuild_WithStore_CleanupSafe characterizes the store-open path: with the
// history gate on, Build opens the store (cfg.Store != nil) and the returned
// cleanup closes it without panicking. Covers the store closer the gates-off
// happy-path test skips.
func TestBuild_WithStore_CleanupSafe(t *testing.T) {
	tmp := t.TempDir()
	home := t.TempDir()
	writeConfigWithHistory(t, tmp)

	cfg, cleanup, err := agentsetup.Build(agentsetup.Options{
		ConfigPath: filepath.Join(tmp, "shell3.lua"),
		CWD:        tmp,
		HomeDir:    home,
		Headless:   true,
	})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if cfg.Store == nil {
		t.Fatal("expected store to be opened with the history gate on")
	}
	cleanup() // closes store + lua + log; must not panic
}

// TestDecisionEnumSync pins the numeric values of luacfg.Decision that the
// ToolGuard bridge in Build relies on (the bare int(d) cast handed to
// chat's guardDecision ladder: Allow=0, Block=1, Cancel=2, Ask=3). If either
// side renumbers, this fails instead of silently misrouting guard verdicts.
func TestDecisionEnumSync(t *testing.T) {
	if luacfg.DecisionAllow != 0 || luacfg.DecisionBlock != 1 ||
		luacfg.DecisionCancel != 2 || luacfg.DecisionAsk != 3 {
		t.Fatalf("luacfg.Decision values drifted from chat's guardDecision ladder: allow=%d block=%d cancel=%d ask=%d",
			luacfg.DecisionAllow, luacfg.DecisionBlock, luacfg.DecisionCancel, luacfg.DecisionAsk)
	}
}
