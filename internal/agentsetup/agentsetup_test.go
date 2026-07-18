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

const minimalYAML = `models:
  main:
    base_url: https://example.test/v1
    api_key: env:TEST_KEY
    model: test-model
    context_window: 1000
`

// writeMinimalConfig writes a tree Build can load: one model referencing an
// env-injected key, and an agent.md selecting it.
func writeMinimalConfig(t *testing.T, dir string) {
	t.Helper()
	writeTree(t, dir, map[string]string{
		"shell3.yaml": minimalYAML,
		"agent.md":    "---\nmodel: main\n---\nyou are a tester\n",
	})
}

// writeSubagentConfig writes a tree with a registered subagent ("researcher")
// beside the main agent.
func writeSubagentConfig(t *testing.T, dir string) {
	t.Helper()
	writeTree(t, dir, map[string]string{
		"shell3.yaml":          minimalYAML,
		"agent.md":             "---\nmodel: main\ntools: [bash]\n---\nyou are a coder\n",
		"agents/researcher.md": "---\ndescription: investigate things\ntools: [bash]\n---\nyou are a researcher\n",
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
	if cfg.ModeLabel != "agent" {
		t.Errorf("default active agent = %q, want %q", cfg.ModeLabel, "agent")
	}
	// The persona is the agent.md body verbatim; the host Environment facts
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
		"shell3.yaml": `models:
  main:
    base_url: http://localhost:8787/v1
    api_key: env:TEST_KEY
    model: test-model
    run_proxy: "touch ` + marker + `"
`,
		"agent.md": "---\nmodel: main\n---\nhi\n",
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
// one Parts hold independent agent state — re-resolving one never changes the
// other (there is no process-global active-agent state).
func TestSessionConfigs_Independent(t *testing.T) {
	parts, cleanup := subagentParts(t)
	defer cleanup()

	a, err := parts.SessionConfig(agentsetup.SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := parts.SessionConfig(agentsetup.SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}

	rt, err := b.SwitchAgent("researcher")
	if err != nil {
		t.Fatal(err)
	}
	b.ApplyActiveAgent(rt)

	if a.ModeLabel != "agent" {
		t.Fatalf("config A's agent changed to %q when B re-resolved", a.ModeLabel)
	}
	if b.ModeLabel != "researcher" {
		t.Fatalf("config B should be researcher, got %q", b.ModeLabel)
	}
}

// TestBuild_MalformedConfig_Errors characterizes the post-log-open error path:
// a present but invalid shell3.yaml resolves (so the log opens), then
// config.Load fails — Build must surface the error.
func TestBuild_MalformedConfig_Errors(t *testing.T) {
	tmp := t.TempDir()
	home := t.TempDir()
	writeTree(t, tmp, map[string]string{
		"shell3.yaml": "models: [not, a, map\n",
		"agent.md":    "---\nmodel: main\n---\nhi\n",
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
// the JSONL runs layout and the ripgrep search recipe — and none of the retired
// CLIs/UUID — all wrapped in a <system-reminder> envelope.
func TestEnvironmentReminder(t *testing.T) {
	rem := agentsetup.EnvironmentReminder("/c", "/root/.shell3_project/runs", "gpt-x", "sess-42")
	if !strings.HasPrefix(rem, "<system-reminder>") || !strings.HasSuffix(rem, "</system-reminder>") {
		t.Fatalf("reminder not wrapped in <system-reminder>:\n%s", rem)
	}
	for _, want := range []string{
		"- model: gpt-x",
		"- session id: sess-42",
		"- config: `/c`",
		".shell3_project/runs/<id>/messages.jsonl",
		"rg <terms> .shell3_project/runs",
		"subagent transcripts are ordinary sessions",
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

// TestAgentRuntime_TaskToolInSchema proves the invariant: the `task` tool
// appears in the main agent's schema iff agents/ is non-empty (delegation is
// inferred, there is no toggle) — and the allowlist (names + descriptions) is
// baked into the tool's subagent_type parameter, which is the model's only
// source for it.
func TestAgentRuntime_TaskToolInSchema(t *testing.T) {
	// --- Tree WITH agents/: task + management tools must be in schema ---
	p, cleanup := subagentParts(t)
	defer cleanup()

	rt, err := p.AgentRuntime("agent")
	if err != nil {
		t.Fatalf("AgentRuntime(agent): %v", err)
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

	// --- Tree WITHOUT agents/: management tools must NOT be in schema ---
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

	rt2, err := parts2.AgentRuntime("agent")
	if err != nil {
		t.Fatalf("AgentRuntime2(agent): %v", err)
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

// TestBuild_PruneFlag pins the prune=false overlay: the model's derived
// prune_at (compact_at*0.6) is zeroed for an agent that opts out, and kept for
// a subagent that leaves the flag unset. Thresholds stay model-level; the flag
// only gates the stage.
func TestBuild_PruneFlag(t *testing.T) {
	tmp := t.TempDir()
	writeTree(t, tmp, map[string]string{
		"shell3.yaml": `models:
  main:
    base_url: https://example.test/v1
    api_key: env:TEST_KEY
    model: test-model
    compact_at: 100000
`,
		"agent.md":             "---\nmodel: main\nprune: false\n---\np\n",
		"agents/inheriting.md": "---\ndescription: d\n---\np\n",
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

	out, err := parts.AgentRuntime("agent")
	if err != nil {
		t.Fatalf("AgentRuntime(agent): %v", err)
	}
	if out.PruneAt != 0 {
		t.Errorf("agent: PruneAt = %d, want 0 (prune=false)", out.PruneAt)
	}
	if out.CompactAt != 100000 {
		t.Errorf("agent: CompactAt = %d, want 100000 (thresholds stay model-level)", out.CompactAt)
	}

	inh, err := parts.AgentRuntime("inheriting")
	if err != nil {
		t.Fatalf("AgentRuntime(inheriting): %v", err)
	}
	if want := 100000 * 60 / 100; inh.PruneAt != want {
		t.Errorf("inheriting: PruneAt = %d, want %d (model default)", inh.PruneAt, want)
	}
}
