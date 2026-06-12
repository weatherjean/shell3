package agentsetup_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/agentsetup"
	"github.com/weatherjean/shell3/internal/chat"
)

// buildConfig composes the single-session path (BuildParts + a headless
// SessionConfig) the tests below exercise. agent "" selects the first declared
// agent. The production front-ends compose these two phases inline.
func buildConfig(opts agentsetup.Options, agent string) (chat.Config, func(), error) {
	parts, cleanup, err := agentsetup.BuildParts(opts)
	if err != nil {
		return chat.Config{}, cleanup, err
	}
	cfg, err := parts.SessionConfig(agentsetup.SessionOptions{
		Agent: agent, WorkDir: opts.CWD, Headless: true,
	})
	if err != nil {
		cleanup()
		return chat.Config{}, func() {}, err
	}
	return cfg, cleanup, nil
}

func TestBuild_MissingConfig_Errors(t *testing.T) {
	tmp := t.TempDir()
	_, _, err := buildConfig(agentsetup.Options{
		ConfigPath: filepath.Join(tmp, "shell3.lua"),
		CWD:        tmp,
		HomeDir:    tmp,
	}, "")
	if err == nil {
		t.Fatal("expected error for missing config, got nil")
	}
}

func TestBuild_LoadsConfig(t *testing.T) {
	tmp := t.TempDir()
	home := t.TempDir()
	writeMinimalConfig(t, tmp)

	cfg, cleanup, err := buildConfig(agentsetup.Options{
		ConfigPath: filepath.Join(tmp, "shell3.lua"),
		CWD:        tmp,
		HomeDir:    home,
	}, "")
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

	cfg, cleanup, err := buildConfig(agentsetup.Options{
		ConfigPath: filepath.Join(tmp, "shell3.lua"),
		CWD:        tmp,
		HomeDir:    t.TempDir(),
	}, "")
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

	cfg, cleanup, err := buildConfig(agentsetup.Options{
		ConfigPath: filepath.Join(tmp, "shell3.lua"),
		CWD:        tmp,
		HomeDir:    t.TempDir(),
	}, "second")
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

	_, _, err := buildConfig(agentsetup.Options{
		ConfigPath: filepath.Join(tmp, "shell3.lua"),
		CWD:        tmp,
		HomeDir:    t.TempDir(),
	}, "nope")
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

	_, cleanup, err := buildConfig(agentsetup.Options{
		ConfigPath: filepath.Join(tmp, "shell3.lua"),
		CWD:        tmp,
		HomeDir:    t.TempDir(),
	}, "")
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

	_, _, err := buildConfig(agentsetup.Options{
		ConfigPath: filepath.Join(tmp, "shell3.lua"),
		CWD:        tmp,
		HomeDir:    home,
	}, "")
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

	cfg, cleanup, err := buildConfig(agentsetup.Options{
		ConfigPath: filepath.Join(tmp, "shell3.lua"),
		CWD:        tmp,
		HomeDir:    home,
	}, "")
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

	cfg, cleanup, err := buildConfig(agentsetup.Options{
		ConfigPath: filepath.Join(tmp, "shell3.lua"),
		CWD:        tmp,
		HomeDir:    home,
	}, "")
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
	})
	if err != nil {
		t.Fatalf("BuildParts: %v", err)
	}
	return parts, cleanup
}

// TestAgentRuntime_NoSpawnTools asserts that an agent with registered subagents
// no longer gets in-process spawn_agent/list_agents tools (subagents are now a
// bash_bg-backgrounded shell3 described in the prompt), but still carries the
// Subagents allowlist and that each allowed subagent resolves a description for
// the delegation context.
func TestAgentRuntime_NoSpawnTools(t *testing.T) {
	p, cleanup := subagentParts(t)
	defer cleanup()

	rt, err := p.AgentRuntime("code")
	if err != nil {
		t.Fatalf("AgentRuntime: %v", err)
	}
	for _, td := range rt.Personality.Tools {
		if td.Name == "spawn_agent" || td.Name == "list_agents" {
			t.Errorf("unexpected in-process tool %q — spawn machinery was removed", td.Name)
		}
	}
	if len(rt.Subagents) != 1 || rt.Subagents[0] != "researcher" {
		t.Errorf("AgentRuntime(\"code\").Subagents = %v, want [researcher]", rt.Subagents)
	}
	desc, ok := p.SubagentDescription("researcher")
	if !ok || desc == "" {
		t.Errorf("SubagentDescription(\"researcher\") = %q,%v; want a non-empty description", desc, ok)
	}
	if _, ok := p.SubagentDescription("ghost"); ok {
		t.Error("SubagentDescription(\"ghost\") returned ok for an unregistered subagent")
	}
}

// TestAgentRuntime_SubagentResolvesAsAgent asserts that a registered subagent
// name passed to AgentRuntime (the `shell3 --agent <subagent>` spawn path)
// resolves the subagent's own config — correct name, and an empty Subagents
// bundle (depth limit 1 — a subagent declares none).
func TestAgentRuntime_SubagentResolvesAsAgent(t *testing.T) {
	p, cleanup := subagentParts(t)
	defer cleanup()

	srt, err := p.AgentRuntime("researcher")
	if err != nil {
		t.Fatalf("AgentRuntime(researcher): %v", err)
	}
	if srt.ModeLabel != "researcher" {
		t.Errorf("ModeLabel = %q, want %q", srt.ModeLabel, "researcher")
	}
	if srt.Personality.Name != "researcher" {
		t.Errorf("Personality.Name = %q, want %q", srt.Personality.Name, "researcher")
	}
	if len(srt.Subagents) != 0 {
		t.Errorf("AgentRuntime(\"researcher\").Subagents = %v, want empty (depth limit 1)", srt.Subagents)
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

// TestSessionConfig_ResolvesSubagentAsAgent asserts that SessionConfig with
// Agent set to a registered subagent name builds the session as that subagent.
func TestSessionConfig_ResolvesSubagentAsAgent(t *testing.T) {
	p, cleanup := subagentParts(t)
	defer cleanup()

	cfg, err := p.SessionConfig(agentsetup.SessionOptions{Agent: "researcher"})
	if err != nil {
		t.Fatalf("SessionConfig with Agent=researcher: %v", err)
	}
	if cfg.ModeLabel != "researcher" {
		t.Errorf("SessionConfig ModeLabel = %q, want %q", cfg.ModeLabel, "researcher")
	}
}

// TestRefreshPromptFor_Subagent asserts that RefreshPromptFor returns the
// subagent's own system prompt when called with a subagent name, covering the
// /clear path for a session running a subagent config.
func TestRefreshPromptFor_Subagent(t *testing.T) {
	p, cleanup := subagentParts(t)
	defer cleanup()

	cfg, err := p.SessionConfig(agentsetup.SessionOptions{Agent: "researcher"})
	if err != nil {
		t.Fatalf("SessionConfig: %v", err)
	}
	prompt := cfg.RefreshPrompt()
	if !strings.Contains(prompt, "you are a researcher") {
		t.Errorf("RefreshPrompt for subagent session = %q, want it to contain %q", prompt, "you are a researcher")
	}
}

// TestAgentRuntime_UnknownErrors asserts that AgentRuntime returns an error for
// a name in neither the agent nor the subagent registry.
func TestAgentRuntime_UnknownErrors(t *testing.T) {
	p, cleanup := subagentParts(t)
	defer cleanup()

	_, err := p.AgentRuntime("ghost")
	if err == nil {
		t.Fatal("expected error for unknown agent, got nil")
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error should name the unknown agent, got: %v", err)
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
