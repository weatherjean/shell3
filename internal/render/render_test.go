package render_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/chat"
	"github.com/weatherjean/shell3/internal/config"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/llm/fakellm"
	"github.com/weatherjean/shell3/internal/render"
	"github.com/weatherjean/shell3/internal/runs"
	"github.com/weatherjean/shell3/internal/shell3"
)

// fixtureRun writes a session to a fresh store root and returns (root, id).
func fixtureRun(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	st, err := runs.Open(root)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	id, err := st.NewSession(runs.Meta{Workdir: "/tmp/work", Model: "kimi-k2"})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "count the go files"},
		{
			Role:             llm.RoleAssistant,
			ReasoningContent: "I should use rg for this.\nIt is faster.",
			ToolCalls: []llm.ToolCall{
				{ID: "call_1", Name: "bash", RawArgs: `{"command":"rg --files -g '*.go' | wc -l"}`},
			},
		},
		{Role: llm.RoleTool, Name: "bash", ToolCallID: "call_1", Content: "[tool_call_id=call_1]\n42"},
		{Role: llm.RoleAssistant, Content: "There are 42 Go files."},
	}
	for _, m := range msgs {
		if err := st.AppendMessage(id, m); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	return root, id
}

func TestRunReplay(t *testing.T) {
	root, id := fixtureRun(t)
	out, err := render.RunReplay(root, id)
	if err != nil {
		t.Fatalf("RunReplay: %v", err)
	}
	for _, want := range []string{
		id,
		"count the go files",
		"tool:",
		"bash",
		`{"command":"rg --files -g '*.go' | wc -l"}`,
		"result:",
		"There are 42 Go files.",
		"> I should use rg for this.",
		"> It is faster.",
		"```",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("RunReplay output missing %q\n---\n%s", want, out)
		}
	}
	if strings.Contains(out, "[tool_call_id=") {
		t.Errorf("RunReplay leaked the tool-call-id prefix:\n%s", out)
	}
	if strings.Contains(out, "<") && strings.Contains(out, "</") {
		t.Errorf("RunReplay emitted HTML:\n%s", out)
	}
}

func TestRunReplayTruncatesHugeToolResult(t *testing.T) {
	root := t.TempDir()
	st, err := runs.Open(root)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	id, err := st.NewSession(runs.Meta{})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	big := strings.Repeat("x", 10_000)
	if err := st.AppendMessage(id, llm.Message{Role: llm.RoleTool, Name: "bash", Content: big}); err != nil {
		t.Fatalf("append: %v", err)
	}
	out, err := render.RunReplay(root, id)
	if err != nil {
		t.Fatalf("RunReplay: %v", err)
	}
	if !strings.Contains(out, "… (truncated)") {
		t.Errorf("expected a truncation marker:\n%s", out)
	}
	if len(out) > 8_000 {
		t.Errorf("output not truncated: %d bytes", len(out))
	}
}

func TestRunReplayUnknownID(t *testing.T) {
	root, _ := fixtureRun(t)
	if _, err := render.RunReplay(root, "no-such-run"); err == nil {
		t.Fatal("expected an error for an unknown run id")
	}
}

// TestRunReplayRejectsTraversalIDs pins the id validator: "." and ".." survive
// filepath.Base unchanged, so the Base equality check alone lets them through
// to a Stat that succeeds on the runs root itself.
func TestRunReplayRejectsTraversalIDs(t *testing.T) {
	root, _ := fixtureRun(t)
	for _, id := range []string{".", "..", "../runs"} {
		_, err := render.RunReplay(root, id)
		if err == nil {
			t.Fatalf("RunReplay(%q): expected an invalid-id error", id)
		}
		if !strings.Contains(err.Error(), "invalid run id") {
			t.Errorf("RunReplay(%q): want an invalid-id error, got %v", id, err)
		}
	}
}

func TestRunsList(t *testing.T) {
	root, id := fixtureRun(t)
	out, err := render.RunsList(root, 10)
	if err != nil {
		t.Fatalf("RunsList: %v", err)
	}
	if !strings.Contains(out, id) {
		t.Errorf("listing missing run id %q:\n%s", id, out)
	}
	if !strings.Contains(out, "count the go files") {
		t.Errorf("listing missing preview:\n%s", out)
	}
}

func TestRunsListEmpty(t *testing.T) {
	out, err := render.RunsList(t.TempDir(), 10)
	if err != nil {
		t.Fatalf("RunsList: %v", err)
	}
	if !strings.Contains(strings.ToLower(out), "no runs") {
		t.Errorf("expected an empty-state line:\n%s", out)
	}
}

func TestStatus(t *testing.T) {
	rt := shell3.RuntimeForTest(t.TempDir(), func(o shell3.SessionOpts) (chat.Config, error) {
		cfg := chat.Config{
			LLM:            fakellm.New(),
			ModeLabel:      "agent",
			AgentNames:     []string{"agent"},
			StatusLine:     chat.FormatStatus("moonshot", "kimi-k2-0905-preview", "high"),
			ActiveSkills:   []string{"scripting"},
			ConfigWarnings: []string{"skill x skipped"},
			RunToolCall: func(context.Context, string, string, string, bool) chat.ToolCallVerdict {
				return chat.ToolCallVerdict{}
			},
		}
		cfg.ContextWindow = 128000
		cfg.Subagents = []string{"explorer"}
		cfg.Personality.SystemPrompt = "You are shell3."
		cfg.Personality.Tools = []llm.ToolDefinition{{Name: "bash", Description: "run a command"}}
		cfg.Headless = o.Headless
		return cfg, nil
	})
	t.Cleanup(func() { _ = rt.Close() })
	sess, err := rt.Session(shell3.SessionOpts{Name: "main"})
	if err != nil {
		t.Fatalf("session: %v", err)
	}

	out := render.Status(sess, rt, "0.2.0")
	for _, want := range []string{
		"kimi-k2-0905-preview",
		"0.2.0",
		"bash",
		"explorer",
		"scripting",
		"skill x skipped",
		"You are shell3.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("Status missing %q\n---\n%s", want, out)
		}
	}
	if !strings.Contains(strings.ToLower(out), "armed") {
		t.Errorf("Status missing the gate line:\n%s", out)
	}
}

func TestStatusNilSafe(t *testing.T) {
	if out := render.Status(nil, nil, "0.2.0"); !strings.Contains(out, "0.2.0") {
		t.Errorf("Status(nil, nil) should still render a header:\n%s", out)
	}
}

func TestJobs(t *testing.T) {
	exit := 0
	now := time.Now().Add(-90 * time.Second)
	list := []shell3.JobInfo{
		{ID: "bg1", Cmd: "make build", Kind: shell3.JobCommand, StartedAt: now},
		{
			ID: "sub1", Agent: "explorer", Cmd: "map the config loader",
			Kind: shell3.JobSubagent, StartedAt: now, Done: true,
			EndedAt: now.Add(30 * time.Second), Summary: "config lives in internal/config",
		},
		{ID: "bg2", Cmd: "go test ./...", Kind: shell3.JobCommand, StartedAt: now, Done: true, Exit: &exit},
	}
	out := render.Jobs(list)
	for _, want := range []string{"bg1", "make build", "sub1", "explorer", "go test ./...", "Running", "Finished"} {
		if !strings.Contains(out, want) {
			t.Errorf("Jobs missing %q\n---\n%s", want, out)
		}
	}
}

func TestJobsEmpty(t *testing.T) {
	if out := render.Jobs(nil); !strings.Contains(strings.ToLower(out), "no background jobs") {
		t.Errorf("expected an empty-state line:\n%s", out)
	}
}

func TestJobDetail(t *testing.T) {
	exit := 2
	info := shell3.JobInfo{
		ID: "bg7", Cmd: "make lint", Kind: shell3.JobCommand,
		StartedAt: time.Now().Add(-time.Minute), Done: true, Exit: &exit,
	}
	out := render.JobDetail(info, "golangci-lint: 3 issues")
	for _, want := range []string{"bg7", "make lint", "exit", "2", "golangci-lint: 3 issues", "```"} {
		if !strings.Contains(out, want) {
			t.Errorf("JobDetail missing %q\n---\n%s", want, out)
		}
	}
}

func TestCron(t *testing.T) {
	last := time.Date(2026, 7, 28, 9, 0, 0, 0, time.UTC)
	jobs := []config.CronJob{
		{
			Name: "digest", Schedule: "0 8 * * *", Agent: "explorer",
			Prompt: "summarise yesterday's commits", WorkDir: "/tmp/repo", Direct: true,
		},
		{Name: "backup", Schedule: "@daily", Agent: "ops", Prompt: "run the backup"},
	}
	out := render.Cron(jobs, map[string]time.Time{"digest": last})
	for _, want := range []string{
		"digest", "0 8 * * *", "explorer", "/tmp/repo",
		"summarise yesterday's commits", "backup", "@daily", "2026-07-28",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("Cron missing %q\n---\n%s", want, out)
		}
	}
	if !strings.Contains(strings.ToLower(out), "direct") {
		t.Errorf("Cron missing the delivery mode:\n%s", out)
	}
}

func TestCronEmpty(t *testing.T) {
	if out := render.Cron(nil, nil); !strings.Contains(strings.ToLower(out), "no cron jobs") {
		t.Errorf("expected an empty-state line:\n%s", out)
	}
}
