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

// writeTree writes a config tree into dir: the given files plus a default
// .env (TEST_KEY) unless the map carries one.
func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	if _, ok := files[".env"]; !ok {
		if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("TEST_KEY=sk-test\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// minimalWiring is the `shell3:` block of the fixtures below: one model
// referencing an env-injected key.
const minimalWiring = `#---
# shell3:
#   models:
#     main:
#       base_url: https://example.test/v1
#       api_key: env:TEST_KEY
#       model: test-model
#       context_window: 1000
#---
`

// kitAgent renders one `agent:` declaration plus its prompt function. extra
// holds additional frontmatter lines (already "# "-prefixed content, one per
// entry) and body is the prompt text.
func kitAgent(name, body string, extra ...string) string {
	var b strings.Builder
	b.WriteString("\n#---\n# agent: " + name + "\n# model: main\n")
	for _, e := range extra {
		b.WriteString("# " + e + "\n")
	}
	b.WriteString("#---\n" + name + "_prompt() { cat <<'SHELL3_EOF'\n" + body + "\nSHELL3_EOF\n}\n")
	return b.String()
}

// minimalKit is the smallest kit BuildParts can load.
var minimalKit = minimalWiring + kitAgent("main", "you are a tester")

// writeMinimalConfig writes a tree Build can load.
func writeMinimalConfig(t *testing.T, dir string) {
	t.Helper()
	writeTree(t, dir, map[string]string{"shell3.sh": minimalKit})
}

// writeSubagentConfig writes a kit with an employee ("researcher") beside the
// main agent.
func writeSubagentConfig(t *testing.T, dir string) {
	t.Helper()
	writeTree(t, dir, map[string]string{
		"shell3.sh": minimalWiring +
			kitAgent("main", "you are a coder", "use: [bash]") +
			kitAgent("researcher", "you are a researcher", "description: investigate things", "use: [bash]"),
	})
}

// buildConfig composes the single-session path (BuildParts + a headless
// SessionConfig) the tests below exercise. agent "" selects the main agent.
// The production front-ends compose these two phases inline.
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
		ConfigDir: tmp,
		CWD:       tmp,
		HomeDir:   tmp,
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
		ConfigDir: tmp,
		CWD:       tmp,
		HomeDir:   home,
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

func TestBuild_Agent_DefaultsToTheAgent(t *testing.T) {
	tmp := t.TempDir()
	writeSubagentConfig(t, tmp)

	cfg, cleanup, err := buildConfig(agentsetup.Options{
		ConfigDir: tmp,
		CWD:       tmp,
		HomeDir:   t.TempDir(),
	}, "")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer cleanup()
	if cfg.ModeLabel != "main" {
		t.Errorf("default active agent = %q, want %q", cfg.ModeLabel, "main")
	}
	// The persona is the kit prompt function's body verbatim; the host Environment facts
	// live in a standing reminder (set by internal/shell3), NOT the system prompt.
	if !strings.HasPrefix(cfg.Personality.SystemPrompt, "you are a coder") {
		t.Errorf("system prompt = %q, want a prefix of the agent's prompt", cfg.Personality.SystemPrompt)
	}
	if strings.Contains(cfg.Personality.SystemPrompt, "## Environment") {
		t.Errorf("system prompt should NOT contain Environment section: %q", cfg.Personality.SystemPrompt)
	}
}

func TestBuild_Agent_UnknownErrors(t *testing.T) {
	tmp := t.TempDir()
	writeSubagentConfig(t, tmp)

	_, _, err := buildConfig(agentsetup.Options{
		ConfigDir: tmp,
		CWD:       tmp,
		HomeDir:   t.TempDir(),
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
	writeTree(t, tmp, map[string]string{
		"shell3.sh": `#---
# shell3:
#   models:
#     main:
#       base_url: http://localhost:8787/v1
#       api_key: env:TEST_KEY
#       model: test-model
#       run_proxy: "touch ` + marker + `"
#---
` + kitAgent("main", "hi"),
	})

	_, cleanup, err := buildConfig(agentsetup.Options{
		ConfigDir: tmp,
		CWD:       tmp,
		HomeDir:   t.TempDir(),
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

// subagentParts builds Parts from writeSubagentConfig in a fresh temp dir.
func subagentParts(t *testing.T) (*agentsetup.Parts, func()) {
	t.Helper()
	tmp := t.TempDir()
	writeSubagentConfig(t, tmp)
	parts, cleanup, err := agentsetup.BuildParts(agentsetup.Options{
		ConfigDir: tmp,
		CWD:       tmp,
		HomeDir:   t.TempDir(),
	})
	if err != nil {
		t.Fatalf("BuildParts: %v", err)
	}
	return parts, cleanup
}

// TestSessionConfigs_Independent pins the invariant: two configs derived from
// one Parts hold independent agent state — deriving one for a different agent
// never changes the other (there is no process-global active-agent state).
func TestSessionConfigs_Independent(t *testing.T) {
	parts, cleanup := subagentParts(t)
	defer cleanup()

	a, err := parts.SessionConfig(agentsetup.SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := parts.SessionConfig(agentsetup.SessionOptions{Agent: "researcher"})
	if err != nil {
		t.Fatal(err)
	}

	if a.ModeLabel != "main" {
		t.Fatalf("config A's agent changed to %q when B re-resolved", a.ModeLabel)
	}
	if b.ModeLabel != "researcher" {
		t.Fatalf("config B should be researcher, got %q", b.ModeLabel)
	}
}

// TestSessionConfig_ContextReadPerSession pins the fresh-turn contract:
// context files are read at session-config build, so an edit landing between
// two sessions is visible to the second without a reload. The first session's
// already-rendered prompt keeps the old body.
func TestSessionConfig_ContextReadPerSession(t *testing.T) {
	tmp := t.TempDir()
	writeTree(t, tmp, map[string]string{
		"shell3.sh": minimalWiring + kitAgent("main", "you are a tester", "context: [memory.md]"),
		"memory.md": "MEMORY-V1",
	})
	parts, cleanup, err := agentsetup.BuildParts(agentsetup.Options{
		ConfigDir: tmp,
		CWD:       tmp,
		HomeDir:   t.TempDir(),
	})
	if err != nil {
		t.Fatalf("BuildParts: %v", err)
	}
	defer cleanup()

	cfg1, err := parts.SessionConfig(agentsetup.SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"## Context", "### memory.md", "MEMORY-V1"} {
		if !strings.Contains(cfg1.Personality.SystemPrompt, want) {
			t.Fatalf("first session prompt missing %q:\n%s", want, cfg1.Personality.SystemPrompt)
		}
	}

	// Edit the brain, then build a second session — it must see the new body.
	if err := os.WriteFile(filepath.Join(tmp, "memory.md"), []byte("MEMORY-V2"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg2, err := parts.SessionConfig(agentsetup.SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cfg2.Personality.SystemPrompt, "MEMORY-V2") {
		t.Errorf("second session prompt missing rewritten body:\n%s", cfg2.Personality.SystemPrompt)
	}
	if strings.Contains(cfg2.Personality.SystemPrompt, "MEMORY-V1") {
		t.Errorf("second session prompt still carries the stale body:\n%s", cfg2.Personality.SystemPrompt)
	}
	// The first session captured the old body at build time.
	if !strings.Contains(cfg1.Personality.SystemPrompt, "MEMORY-V1") {
		t.Errorf("first session prompt should retain the old body:\n%s", cfg1.Personality.SystemPrompt)
	}
}

// TestBuild_MalformedConfig_Errors characterizes the post-log-open error path:
// a present but invalid kit resolves (so the log opens), then config.Load
// fails — Build must surface the error.
func TestBuild_MalformedConfig_Errors(t *testing.T) {
	tmp := t.TempDir()
	home := t.TempDir()
	writeTree(t, tmp, map[string]string{
		"shell3.sh": "#---\n# shell3:\n#   models: [not, a, map\n#---\n",
	})

	_, _, err := buildConfig(agentsetup.Options{
		ConfigDir: tmp,
		CWD:       tmp,
		HomeDir:   home,
	}, "")
	if err == nil {
		t.Fatal("expected error for malformed config, got nil")
	}
}

// TestBuild_AlwaysOpensStore characterizes the store-open path: the store is
// opened unconditionally so the conversation always persists (saveHistory) and
// the agent can read it back with rg/cat.
func TestBuild_AlwaysOpensStore(t *testing.T) {
	tmp := t.TempDir()
	home := t.TempDir()
	writeMinimalConfig(t, tmp)

	cfg, cleanup, err := buildConfig(agentsetup.Options{
		ConfigDir: tmp,
		CWD:       tmp,
		HomeDir:   home,
	}, "")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if cfg.Store == nil {
		t.Fatal("expected store to be opened unconditionally, got nil")
	}
	cleanup() // closes store + config + log; must not panic
}

// TestEnvironmentReminder asserts the host Environment standing reminder (no
// longer part of the system prompt) carries the model, session id, config dir,
// the database path and the history-tool recall pointer — and none of the
// retired CLIs/UUID — all wrapped in a <system-reminder> envelope.
func TestEnvironmentReminder(t *testing.T) {
	rem := agentsetup.EnvironmentReminder("/c", "/root/.shell3_project/runs", "gpt-x", "sess-42")
	if !strings.HasPrefix(rem, "<system-reminder>") || !strings.HasSuffix(rem, "</system-reminder>") {
		t.Fatalf("reminder not wrapped in <system-reminder>:\n%s", rem)
	}
	for _, want := range []string{
		"- model: gpt-x",
		"- session id: sess-42",
		"- config: `/c`",
		".shell3_project/shell3.db",
		"history tool",
		".shell3_project/runs/<session>/jobs/<job>.log",
	} {
		if !strings.Contains(rem, want) {
			t.Errorf("Environment reminder missing %q:\n%s", want, rem)
		}
	}
	for _, gone := range []string{"shell3 fts", "shell3 list-projects", "shell3 read-session", "project_uuid", "runs/jobs"} {
		if strings.Contains(rem, gone) {
			t.Errorf("Environment reminder still advertises retired %q:\n%s", gone, rem)
		}
	}
	// Empty runs dir → no reminder (never advertise an unusable path).
	if got := agentsetup.EnvironmentReminder("/c", "", "gpt-x", "sess-42"); got != "" {
		t.Errorf("EnvironmentReminder with empty runsDir = %q, want empty", got)
	}
}

// TestAgentRuntime_SubagentResolvesAsAgent asserts that a registered subagent
// name passed to AgentRuntime (the task-tool spawn path) resolves the
// subagent's own config — correct name, and an empty Subagents bundle
// (delegation is single-level by construction).
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
		t.Errorf("AgentRuntime(\"researcher\").Subagents = %v, want empty (single-level)", srt.Subagents)
	}
	// A subagent never gets the task family.
	for _, td := range srt.Personality.Tools {
		if td.Name == "task" {
			t.Error("subagent runtime must not carry the task tool")
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
// employee's own system prompt when called with an employee name — the
// per-turn refresh path for a session running an employee's config.
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

// TestAgentRuntime_TaskToolInSchema proves the invariant: the `task` tool
// appears in the main agent's schema iff the kit declares an employee
// (delegation is inferred, there is no toggle) — and the allowlist (names + descriptions) is
// baked into the tool's subagent_type parameter, which is the model's only
// source for it.
func TestAgentRuntime_TaskToolInSchema(t *testing.T) {
	// --- Kit WITH an employee: task + management tools must be in schema ---
	p, cleanup := subagentParts(t)
	defer cleanup()

	rt, err := p.AgentRuntime("main")
	if err != nil {
		t.Fatalf("AgentRuntime(main): %v", err)
	}
	toolSet := make(map[string]bool)
	for _, td := range rt.Personality.Tools {
		toolSet[td.Name] = true
	}
	for _, want := range []string{"task", "task_list", "task_status", "task_cancel"} {
		if !toolSet[want] {
			t.Errorf("agent with subagents should have %q in its tool schema", want)
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

	// --- Kit with only a main agent: management tools must NOT be in schema ---
	tmp2 := t.TempDir()
	writeMinimalConfig(t, tmp2)
	parts2, cleanup2, err := agentsetup.BuildParts(agentsetup.Options{
		ConfigDir: tmp2,
		CWD:       tmp2,
		HomeDir:   t.TempDir(),
	})
	if err != nil {
		t.Fatalf("BuildParts2: %v", err)
	}
	defer cleanup2()

	rt2, err := parts2.AgentRuntime("main")
	if err != nil {
		t.Fatalf("AgentRuntime2(main): %v", err)
	}
	for _, td := range rt2.Personality.Tools {
		for _, mgmt := range []string{"task", "task_list", "task_status", "task_cancel"} {
			if td.Name == mgmt {
				t.Errorf("agent with no subagents should NOT have %q in its tool schema", mgmt)
			}
		}
	}
}

// TestAgentRuntime_UnknownErrors asserts that AgentRuntime returns an error
// for a name the kit does not declare.
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
