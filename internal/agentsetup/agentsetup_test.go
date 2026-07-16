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

// writeNamedAgentConfig writes a config with one agent ("first") and one
// subagent ("helper") sharing one model, for exercising agent resolution.
func writeNamedAgentConfig(t *testing.T, dir string) {
	t.Helper()
	lua := `
shell3.model("main", {
  base_url = "https://example.test/v1",
  api_key = shell3.env.secret("TEST_KEY"),
  model = "test-model",
  context_window = 1000,
})
shell3.subagent({ name = "helper", description = "d", model = "main", prompt = "you are helper", tools = {} })
shell3.agent({ name = "first",  model = "main", prompt = "you are first",  tools = {} })
`
	if err := os.WriteFile(filepath.Join(dir, "shell3.lua"), []byte(lua), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("TEST_KEY=sk-test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestBuild_Agent_DefaultsToTheAgent(t *testing.T) {
	tmp := t.TempDir()
	writeNamedAgentConfig(t, tmp)

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
	// The persona is the agent's verbatim Lua prompt; the host Environment facts
	// now live in a standing reminder (set by internal/shell3), NOT the system prompt.
	if !strings.HasPrefix(cfg.Personality.SystemPrompt, "you are first") {
		t.Errorf("system prompt = %q, want a prefix of the agent's prompt", cfg.Personality.SystemPrompt)
	}
	if strings.Contains(cfg.Personality.SystemPrompt, "## Environment") {
		t.Errorf("system prompt should NOT contain Environment section: %q", cfg.Personality.SystemPrompt)
	}
}

func TestBuild_Agent_UnknownErrors(t *testing.T) {
	tmp := t.TempDir()
	writeNamedAgentConfig(t, tmp)

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

// namedAgentOptions writes the one-agent config ("first", plus subagent
// "helper") via writeNamedAgentConfig and returns Options pointing at it, with
// a fresh isolated HomeDir so no real ~/.shell3 is touched.
func namedAgentOptions(t *testing.T) agentsetup.Options {
	t.Helper()
	tmp := t.TempDir()
	writeNamedAgentConfig(t, tmp)
	return agentsetup.Options{
		ConfigPath: filepath.Join(tmp, "shell3.lua"),
		CWD:        tmp,
		HomeDir:    t.TempDir(),
	}
}

// TestSessionConfigs_Independent pins the invariant: two configs derived from
// one Parts hold independent agent state — re-resolving one never changes the
// other (there is no process-global active-agent state).
func TestSessionConfigs_Independent(t *testing.T) {
	parts, cleanup, err := agentsetup.BuildParts(namedAgentOptions(t))
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

	rt, err := b.SwitchAgent("first")
	if err != nil {
		t.Fatal(err)
	}
	b.ApplyActiveAgent(rt)

	if a.ModeLabel != "first" {
		t.Fatalf("config A's agent changed to %q when B re-resolved", a.ModeLabel)
	}
	if b.ModeLabel != "first" {
		t.Fatalf("config B should be first, got %q", b.ModeLabel)
	}
	// Each config renders its own prompt independently.
	if a.RefreshPrompt() != b.RefreshPrompt() {
		t.Fatal("both configs run the same agent; prompts should match")
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

// TestEnvironmentReminder asserts the host Environment standing reminder (no
// longer part of the system prompt) carries the model, session id, config path,
// the JSONL runs layout and the ripgrep search recipe — and none of the retired
// CLIs/UUID — all wrapped in a <system-reminder> envelope.
func TestEnvironmentReminder(t *testing.T) {
	rem := agentsetup.EnvironmentReminder("/c/shell3.lua", "/root/.shell3_project/runs", "gpt-x", "sess-42")
	if !strings.HasPrefix(rem, "<system-reminder>") || !strings.HasSuffix(rem, "</system-reminder>") {
		t.Fatalf("reminder not wrapped in <system-reminder>:\n%s", rem)
	}
	for _, want := range []string{
		"- model: gpt-x",
		"- session id: sess-42",
		"- config: `/c/shell3.lua`",
		".shell3_project/runs/<id>/messages.jsonl",
		"rg <terms> .shell3_project/runs",
		".shell3_project/runs/jobs/",
	} {
		if !strings.Contains(rem, want) {
			t.Errorf("Environment reminder missing %q:\n%s", want, rem)
		}
	}
	for _, gone := range []string{"shell3 fts", "shell3 list-projects", "shell3 read-session", "project_uuid"} {
		if strings.Contains(rem, gone) {
			t.Errorf("Environment reminder still advertises retired %q:\n%s", gone, rem)
		}
	}
	// Empty runs dir → no reminder (never advertise an unusable path).
	if got := agentsetup.EnvironmentReminder("/c/shell3.lua", "", "gpt-x", "sess-42"); got != "" {
		t.Errorf("EnvironmentReminder with empty runsDir = %q, want empty", got)
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
// Subagents allowlist.
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

// writeSubagentConfigWithDelegation writes a config with an agent that has both
// a subagent allowlist AND delegation=true, so the `task` tool gate fires.
func writeSubagentConfigWithDelegation(t *testing.T, dir string) {
	t.Helper()
	lua := `
shell3.model("main", {
  base_url = "https://example.test/v1",
  api_key = shell3.env.secret("TEST_KEY"),
  model = "test-model",
  context_window = 1000,
})
local r = shell3.subagent({ name = "researcher", description = "investigate things", model = "main", prompt = "you are a researcher", tools = { bash = true } })
shell3.agent({ name = "code", model = "main", prompt = "you are a coder", tools = { bash = true, subagents = { r } }, delegation = true })
`
	if err := os.WriteFile(filepath.Join(dir, "shell3.lua"), []byte(lua), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("TEST_KEY=sk-test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestAgentRuntime_TaskToolInSchema proves the invariant: the `task` tool
// appears in an agent's schema iff it has delegation=true AND a non-empty
// Subagents allowlist — and the allowlist (names + descriptions) is baked
// into the tool's subagent_type parameter, which is the model's only source
// for it (there is no delegation reminder).
func TestAgentRuntime_TaskToolInSchema(t *testing.T) {
	// --- Agent WITH delegation=true + subagents: task + management tools must be in schema ---
	tmp := t.TempDir()
	writeSubagentConfigWithDelegation(t, tmp)
	parts, cleanup, err := agentsetup.BuildParts(agentsetup.Options{
		ConfigPath: filepath.Join(tmp, "shell3.lua"),
		CWD:        tmp,
		HomeDir:    t.TempDir(),
	})
	if err != nil {
		t.Fatalf("BuildParts: %v", err)
	}
	defer cleanup()

	rt, err := parts.AgentRuntime("code")
	if err != nil {
		t.Fatalf("AgentRuntime(code): %v", err)
	}
	toolSet := make(map[string]bool)
	for _, td := range rt.Personality.Tools {
		toolSet[td.Name] = true
	}
	for _, want := range []string{"task", "task_list", "task_status", "task_cancel"} {
		if !toolSet[want] {
			t.Errorf("agent with delegation=true + subagents should have %q in its tool schema", want)
		}
	}
	for _, td := range rt.Personality.Tools {
		if td.Name != "task" {
			continue
		}
		st := td.Parameters["properties"].(map[string]any)["subagent_type"].(map[string]any)
		if enum, _ := st["enum"].([]string); len(enum) != 1 || enum[0] != "researcher" {
			t.Errorf("task subagent_type enum = %v, want [researcher]", st["enum"])
		}
		if desc, _ := st["description"].(string); !strings.Contains(desc, "researcher: investigate things") {
			t.Errorf("task subagent_type description missing the allowlist entry:\n%s", desc)
		}
	}

	// --- Agent WITHOUT subagents (minimal config): management tools must NOT be in schema ---
	tmp2 := t.TempDir()
	writeMinimalConfig(t, tmp2)
	parts2, cleanup2, err := agentsetup.BuildParts(agentsetup.Options{
		ConfigPath: filepath.Join(tmp2, "shell3.lua"),
		CWD:        tmp2,
		HomeDir:    t.TempDir(),
	})
	if err != nil {
		t.Fatalf("BuildParts2: %v", err)
	}
	defer cleanup2()

	rt2, err := parts2.AgentRuntime("tester")
	if err != nil {
		t.Fatalf("AgentRuntime2(tester): %v", err)
	}
	for _, td := range rt2.Personality.Tools {
		for _, mgmt := range []string{"task", "task_list", "task_status", "task_cancel"} {
			if td.Name == mgmt {
				t.Errorf("agent with no subagents should NOT have %q in its tool schema", mgmt)
			}
		}
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
