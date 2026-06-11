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
	// The persona leads with the agent's verbatim prompt; the host appends a
	// "## Environment" section (history DB path, etc.) after it.
	if !strings.HasPrefix(cfg.Personality.SystemPrompt, "you are second") {
		t.Errorf("system prompt = %q, want a prefix of the second agent's prompt", cfg.Personality.SystemPrompt)
	}
	if !strings.Contains(cfg.Personality.SystemPrompt, "## Environment") {
		t.Errorf("system prompt missing Environment section: %q", cfg.Personality.SystemPrompt)
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

// TestBuild_AlwaysOpensStore characterizes the store-open path: history is no
// longer gated — the store is opened unconditionally so the conversation always
// persists (saveHistory) and the agent can read it back via the `history` skill.
// A plain minimal config (no history gate; the gate no longer exists) must still
// come up with cfg.Store != nil, and cleanup must close it without panicking.
func TestBuild_AlwaysOpensStore(t *testing.T) {
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
	if cfg.Store == nil {
		t.Fatal("expected store to be opened unconditionally, got nil")
	}
	cleanup() // closes store + lua + log; must not panic
}

// TestBuild_PromptHasEnvironmentSection asserts the host appends an
// "## Environment" section carrying the read-only history DB path to the
// agent's system prompt — the runtime value the `history` skill needs to open
// `sqlite3 'file:<db>?mode=ro'`. The path must be the real project DB and the
// section must show the ro-open form.
func TestBuild_PromptHasEnvironmentSection(t *testing.T) {
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

	prompt := cfg.Personality.SystemPrompt
	if !strings.Contains(prompt, "## Environment") {
		t.Fatalf("prompt missing Environment section:\n%s", prompt)
	}
	// The DB lives under the per-project dir in HomeDir; assert the path and the
	// read-only open form both appear.
	wantPathFragment := filepath.Join(home, ".shell3", "projects")
	if !strings.Contains(prompt, wantPathFragment) {
		t.Errorf("Environment section missing project DB path %q:\n%s", wantPathFragment, prompt)
	}
	if !strings.Contains(prompt, "shell3.db") || !strings.Contains(prompt, "?mode=ro") {
		t.Errorf("Environment section missing ro history DB open form:\n%s", prompt)
	}
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

// writeSubagentConfig writes a config with a registered subagent ("researcher")
// and an agent ("code") that lists it, plus the required .env.
func writeSubagentConfig(t *testing.T, dir string) {
	t.Helper()
	lua := `
shell3.model("main", {
  base_url = "https://example.test/v1",
  api_key = shell3.env.secret("TEST_KEY"),
  model = "test-model",
  context_window = 1000,
})
local r = shell3.subagent({ name = "researcher", description = "investigate things", model = "main", prompt = "you are a researcher", tools = { bash = true } })
shell3.agent({ name = "code", model = "main", prompt = "you are a coder", tools = { bash = true, subagents = { r } } })
`
	if err := os.WriteFile(filepath.Join(dir, "shell3.lua"), []byte(lua), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("TEST_KEY=sk-test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// subagentParts builds Parts from writeSubagentConfig in a fresh temp dir.
func subagentParts(t *testing.T) (*agentsetup.Parts, func()) {
	t.Helper()
	tmp := t.TempDir()
	writeSubagentConfig(t, tmp)
	parts, cleanup, err := agentsetup.BuildParts(agentsetup.Options{
		ConfigPath: filepath.Join(tmp, "shell3.lua"),
		CWD:        tmp,
		HomeDir:    t.TempDir(),
		Headless:   true,
	})
	if err != nil {
		t.Fatalf("BuildParts: %v", err)
	}
	return parts, cleanup
}

// TestAgentRuntime_ExposesRegisteredSubagents asserts that an agent whose
// Subagents list is non-empty gets spawn_agent and list_agents injected into
// its schema, with spawn_agent's subagent enum matching the registered names.
func TestAgentRuntime_ExposesRegisteredSubagents(t *testing.T) {
	p, cleanup := subagentParts(t)
	defer cleanup()

	rt, err := p.AgentRuntime("code")
	if err != nil {
		t.Fatalf("AgentRuntime: %v", err)
	}

	hasSpawn := false
	hasListAgents := false
	for _, td := range rt.Personality.Tools {
		switch td.Name {
		case "spawn_agent":
			hasSpawn = true
			// Drill into spawn_agent's subagent enum.
			props, ok := td.Parameters["properties"].(map[string]any)
			if !ok {
				t.Fatal("spawn_agent: Parameters[\"properties\"] is not map[string]any")
			}
			subagentProp, ok := props["subagent"].(map[string]any)
			if !ok {
				t.Fatal("spawn_agent: properties[\"subagent\"] is not map[string]any")
			}
			enum, ok := subagentProp["enum"].([]string)
			if !ok {
				t.Fatalf("spawn_agent: subagent enum is %T, want []string", subagentProp["enum"])
			}
			if len(enum) != 1 || enum[0] != "researcher" {
				t.Errorf("spawn_agent subagent enum = %v, want [researcher]", enum)
			}
		case "list_agents":
			hasListAgents = true
		}
	}
	if !hasSpawn {
		t.Error("spawn_agent not found in Personality.Tools")
	}
	if !hasListAgents {
		t.Error("list_agents not found in Personality.Tools")
	}
	// ActiveTools must also contain both names.
	activeSpawn := false
	activeList := false
	for _, n := range rt.ActiveTools {
		if n == "spawn_agent" {
			activeSpawn = true
		}
		if n == "list_agents" {
			activeList = true
		}
	}
	if !activeSpawn {
		t.Error("spawn_agent not found in ActiveTools")
	}
	if !activeList {
		t.Error("list_agents not found in ActiveTools")
	}
}

// TestAgentRuntime_SubagentsAllowlist asserts that an agent's runtime bundle
// carries the allowlist of registered subagent names in Subagents, and that a
// subagent's own bundle has an empty Subagents (depth limit 1).
func TestAgentRuntime_SubagentsAllowlist(t *testing.T) {
	p, cleanup := subagentParts(t)
	defer cleanup()

	rt, err := p.AgentRuntime("code")
	if err != nil {
		t.Fatalf("AgentRuntime: %v", err)
	}
	if len(rt.Subagents) != 1 || rt.Subagents[0] != "researcher" {
		t.Errorf("AgentRuntime(\"code\").Subagents = %v, want [researcher]", rt.Subagents)
	}

	srt, err := p.SubagentRuntime("researcher")
	if err != nil {
		t.Fatalf("SubagentRuntime: %v", err)
	}
	if len(srt.Subagents) != 0 {
		t.Errorf("SubagentRuntime(\"researcher\").Subagents = %v, want empty (depth limit 1)", srt.Subagents)
	}
}

// TestAgentRuntime_NoSubagentsNoSpawnTool asserts that an agent with no
// Subagents list gets neither spawn_agent nor list_agents in its schema.
func TestAgentRuntime_NoSubagentsNoSpawnTool(t *testing.T) {
	tmp := t.TempDir()
	writeMinimalConfig(t, tmp)
	parts, cleanup, err := agentsetup.BuildParts(agentsetup.Options{
		ConfigPath: filepath.Join(tmp, "shell3.lua"),
		CWD:        tmp,
		HomeDir:    t.TempDir(),
		Headless:   true,
	})
	if err != nil {
		t.Fatalf("BuildParts: %v", err)
	}
	defer cleanup()

	rt, err := parts.AgentRuntime("tester")
	if err != nil {
		t.Fatalf("AgentRuntime: %v", err)
	}
	for _, td := range rt.Personality.Tools {
		if td.Name == "spawn_agent" || td.Name == "list_agents" {
			t.Errorf("unexpected tool %q in agent with no subagents", td.Name)
		}
	}
	for _, n := range rt.ActiveTools {
		if n == "spawn_agent" || n == "list_agents" {
			t.Errorf("unexpected active tool %q in agent with no subagents", n)
		}
	}
}

// TestSessionConfig_ResolvesSubagentConfig asserts that a SessionOptions with
// Subagent set builds the session as the subagent (correct name) and that the
// resulting runtime has no spawn tooling (depth limit 1).
func TestSessionConfig_ResolvesSubagentConfig(t *testing.T) {
	p, cleanup := subagentParts(t)
	defer cleanup()

	rt, err := p.SubagentRuntime("researcher")
	if err != nil {
		t.Fatalf("SubagentRuntime: %v", err)
	}

	if rt.Personality.Name != "researcher" {
		t.Errorf("Personality.Name = %q, want %q", rt.Personality.Name, "researcher")
	}
	if rt.ModeLabel != "researcher" {
		t.Errorf("ModeLabel = %q, want %q", rt.ModeLabel, "researcher")
	}
	for _, td := range rt.Personality.Tools {
		if td.Name == "spawn_agent" || td.Name == "list_agents" {
			t.Errorf("subagent runtime should not have %q (depth limit 1)", td.Name)
		}
	}
	// Also check SessionConfig routes to SubagentRuntime when Subagent is set.
	cfg, err := p.SessionConfig(agentsetup.SessionOptions{Subagent: "researcher"})
	if err != nil {
		t.Fatalf("SessionConfig with Subagent: %v", err)
	}
	if cfg.ModeLabel != "researcher" {
		t.Errorf("SessionConfig ModeLabel = %q, want %q", cfg.ModeLabel, "researcher")
	}
}

// TestRefreshPromptFor_Subagent asserts that RefreshPromptFor returns the
// subagent's own system prompt when called with a subagent name, covering the
// /clear path for spawned-subagent sessions (Fix 2 regression guard).
func TestRefreshPromptFor_Subagent(t *testing.T) {
	p, cleanup := subagentParts(t)
	defer cleanup()

	// Build a subagent session so activeName == "researcher".
	cfg, err := p.SessionConfig(agentsetup.SessionOptions{Subagent: "researcher"})
	if err != nil {
		t.Fatalf("SessionConfig: %v", err)
	}
	prompt := cfg.RefreshPrompt()
	if !strings.Contains(prompt, "you are a researcher") {
		t.Errorf("RefreshPrompt for subagent session = %q, want it to contain %q", prompt, "you are a researcher")
	}
}

// TestSubagentRuntime_UnknownErrors asserts that SubagentRuntime returns an
// error for a name that is not registered.
func TestSubagentRuntime_UnknownErrors(t *testing.T) {
	p, cleanup := subagentParts(t)
	defer cleanup()

	_, err := p.SubagentRuntime("ghost")
	if err == nil {
		t.Fatal("expected error for unknown subagent, got nil")
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error should name the unknown subagent, got: %v", err)
	}
}

func TestResolveTelegramConfigPath(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	tgDir := filepath.Join(home, ".shell3", "telegram")
	if err := os.MkdirAll(tgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	tgCfg := filepath.Join(tgDir, "shell3.lua")
	globalCfg := filepath.Join(home, ".shell3", "shell3.lua")
	localCfg := filepath.Join(cwd, "shell3.lua")
	write := func(p string) {
		if err := os.WriteFile(p, []byte("-- x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Explicit flag always wins.
	if got, err := agentsetup.ResolveTelegramConfigPath("/explicit/x.lua", cwd, home); err != nil || got != "/explicit/x.lua" {
		t.Fatalf("flag: got %q err %v", got, err)
	}
	// Nothing exists yet -> error.
	if _, err := agentsetup.ResolveTelegramConfigPath("", cwd, home); err == nil {
		t.Fatal("expected error when no config exists")
	}
	// Only project-local exists -> it.
	write(localCfg)
	if got, _ := agentsetup.ResolveTelegramConfigPath("", cwd, home); got != localCfg {
		t.Fatalf("local: got %q want %q", got, localCfg)
	}
	// Global beats project-local.
	write(globalCfg)
	if got, _ := agentsetup.ResolveTelegramConfigPath("", cwd, home); got != globalCfg {
		t.Fatalf("global: got %q want %q", got, globalCfg)
	}
	// Telegram dir beats everything (except an explicit flag).
	write(tgCfg)
	if got, _ := agentsetup.ResolveTelegramConfigPath("", cwd, home); got != tgCfg {
		t.Fatalf("telegram: got %q want %q", got, tgCfg)
	}
}
