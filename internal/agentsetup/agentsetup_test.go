package agentsetup_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/agentsetup"
	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/llm"
)

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

func kitAgent(name, body string, extra ...string) string {
	var b strings.Builder
	b.WriteString("\n#---\n# agent: " + name + "\n# model: main\n")
	for _, e := range extra {
		b.WriteString("# " + e + "\n")
	}
	b.WriteString("#---\n" + name + "_prompt() { cat <<'SHELL3_EOF'\n" + body + "\nSHELL3_EOF\n}\n")
	return b.String()
}

var minimalKit = minimalWiring + kitAgent("main", "you are a tester")

func writeMinimalConfig(t *testing.T, dir string) {
	t.Helper()
	writeTree(t, dir, map[string]string{"shell3.sh": minimalKit})
}

func writeSubagentConfig(t *testing.T, dir string) {
	t.Helper()
	writeTree(t, dir, map[string]string{
		"shell3.sh": minimalWiring +
			kitAgent("main", "you are a coder", "use: [bash]") +
			kitAgent("researcher", "you are a researcher", "description: investigate things", "use: [bash]"),
	})
}

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
	if cfg.Profile.SystemPrompt == "" {
		t.Error("cfg.Profile.SystemPrompt is empty")
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
	if cfg.Agent != "main" {
		t.Errorf("default active agent = %q, want %q", cfg.Agent, "main")
	}
	if !strings.HasPrefix(cfg.Profile.SystemPrompt, "you are a coder") {
		t.Errorf("system prompt = %q, want a prefix of the agent's prompt", cfg.Profile.SystemPrompt)
	}
	if strings.Contains(cfg.Profile.SystemPrompt, "## Environment") {
		t.Errorf("system prompt should NOT contain Environment section: %q", cfg.Profile.SystemPrompt)
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
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("run_proxy command was not spawned on model activation")
}

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

	if a.Agent != "main" {
		t.Fatalf("config A's agent changed to %q when B re-resolved", a.Agent)
	}
	if b.Agent != "researcher" {
		t.Fatalf("config B should be researcher, got %q", b.Agent)
	}
}

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
		if !strings.Contains(cfg1.Profile.SystemPrompt, want) {
			t.Fatalf("first session prompt missing %q:\n%s", want, cfg1.Profile.SystemPrompt)
		}
	}

	if err := os.WriteFile(filepath.Join(tmp, "memory.md"), []byte("MEMORY-V2"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg2, err := parts.SessionConfig(agentsetup.SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cfg2.Profile.SystemPrompt, "MEMORY-V2") {
		t.Errorf("second session prompt missing rewritten body:\n%s", cfg2.Profile.SystemPrompt)
	}
	if strings.Contains(cfg2.Profile.SystemPrompt, "MEMORY-V1") {
		t.Errorf("second session prompt still carries the stale body:\n%s", cfg2.Profile.SystemPrompt)
	}
	if !strings.Contains(cfg1.Profile.SystemPrompt, "MEMORY-V1") {
		t.Errorf("first session prompt should retain the old body:\n%s", cfg1.Profile.SystemPrompt)
	}
}

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
	cleanup()
}

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
	if got := agentsetup.EnvironmentReminder("/c", "", "gpt-x", "sess-42"); got != "" {
		t.Errorf("EnvironmentReminder with empty runsDir = %q, want empty", got)
	}
}

func TestAgentRuntime_SubagentResolvesAsAgent(t *testing.T) {
	p, cleanup := subagentParts(t)
	defer cleanup()

	srt, err := p.AgentRuntime("researcher")
	if err != nil {
		t.Fatalf("AgentRuntime(researcher): %v", err)
	}
	if srt.Agent != "researcher" {
		t.Errorf("Agent = %q, want %q", srt.Agent, "researcher")
	}
	if len(srt.Subagents) != 0 {
		t.Errorf("AgentRuntime(\"researcher\").Subagents = %v, want empty — no peer to dispatch", srt.Subagents)
	}
	for _, td := range srt.Profile.Tools {
		if td.Name == "task" {
			t.Error("an employee with no peer must not carry the task tool — its enum would be empty")
		}
	}
}

func TestSessionConfig_ResolvesSubagentAsAgent(t *testing.T) {
	p, cleanup := subagentParts(t)
	defer cleanup()

	cfg, err := p.SessionConfig(agentsetup.SessionOptions{Agent: "researcher"})
	if err != nil {
		t.Fatalf("SessionConfig with Agent=researcher: %v", err)
	}
	if cfg.Agent != "researcher" {
		t.Errorf("SessionConfig Agent = %q, want %q", cfg.Agent, "researcher")
	}
}

func TestSubagentWorkdir_ResolvesRelativeToConfigDir(t *testing.T) {
	configDir := t.TempDir()
	home := t.TempDir()
	writeTree(t, configDir, map[string]string{
		"shell3.sh": minimalWiring +
			kitAgent("main", "you are a coder", "use: [bash]") +
			kitAgent("researcher", "you are a researcher",
				"description: investigate things", "workdir: projects/researcher", "use: [bash]"),
	})
	parts, cleanup, err := agentsetup.BuildParts(agentsetup.Options{
		ConfigDir: configDir,
		CWD:       t.TempDir(),
		HomeDir:   home,
	})
	if err != nil {
		t.Fatalf("BuildParts: %v", err)
	}
	defer cleanup()

	want := filepath.Join(configDir, "projects", "researcher")
	if got := parts.SubagentWorkdir("researcher"); got != want {
		t.Errorf("SubagentWorkdir(researcher) = %q, want %q", got, want)
	}
}

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

func TestAgentRuntime_TaskToolInSchema(t *testing.T) {
	p, cleanup := subagentParts(t)
	defer cleanup()

	rt, err := p.AgentRuntime("main")
	if err != nil {
		t.Fatalf("AgentRuntime(main): %v", err)
	}
	toolSet := make(map[string]bool)
	for _, td := range rt.Profile.Tools {
		toolSet[td.Name] = true
	}
	for _, want := range []string{"task", "task_list", "task_status", "task_cancel"} {
		if !toolSet[want] {
			t.Errorf("agent with subagents should have %q in its tool schema", want)
		}
	}
	for _, td := range rt.Profile.Tools {
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
	for _, td := range rt2.Profile.Tools {
		for _, mgmt := range []string{"task", "task_list", "task_status", "task_cancel"} {
			if td.Name == mgmt {
				t.Errorf("agent with no subagents should NOT have %q in its tool schema", mgmt)
			}
		}
	}
}

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

func TestAgentRuntime_EmployeesGetTaskToo(t *testing.T) {
	tmp := t.TempDir()
	writeTree(t, tmp, map[string]string{
		"shell3.sh": minimalWiring +
			kitAgent("main", "you are a coder", "use: [bash]") +
			kitAgent("researcher", "you are a researcher", "description: investigate things", "use: [bash]") +
			kitAgent("writer", "you are a writer", "description: draft things", "use: [bash]"),
	})
	p, cleanup, err := agentsetup.BuildParts(agentsetup.Options{ConfigDir: tmp, CWD: tmp, HomeDir: t.TempDir()})
	if err != nil {
		t.Fatalf("BuildParts: %v", err)
	}
	defer cleanup()

	rt, err := p.AgentRuntime("researcher")
	if err != nil {
		t.Fatalf("AgentRuntime(researcher): %v", err)
	}
	var task *llm.ToolDefinition
	for i, td := range rt.Profile.Tools {
		if td.Name == "task" {
			task = &rt.Profile.Tools[i]
		}
	}
	if task == nil {
		t.Fatalf("an employee with another employee to dispatch must have task; tools = %v", toolNames(rt))
	}
	st := task.Parameters["properties"].(map[string]any)["subagent_type"].(map[string]any)
	enum, _ := st["enum"].([]string)
	if len(enum) != 1 || enum[0] != "writer" {
		t.Fatalf("researcher's task targets = %v, want [writer] — never main, never itself", enum)
	}
}

func toolNames(rt chat.ActiveAgent) []string {
	out := make([]string, 0, len(rt.Profile.Tools))
	for _, td := range rt.Profile.Tools {
		out = append(out, td.Name)
	}
	return out
}
