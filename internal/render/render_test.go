package render_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/weatherjean/shell3/internal/chat"
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

// fixtureRuns writes n minimal sessions and returns (root, ids oldest-first).
func fixtureRuns(t *testing.T, n int) (string, []string) {
	t.Helper()
	root := t.TempDir()
	st, err := runs.Open(root)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	ids := make([]string, n)
	for i := range ids {
		id, err := st.NewSession(runs.Meta{Workdir: "/tmp/work", Model: "kimi-k2"})
		if err != nil {
			t.Fatalf("new session: %v", err)
		}
		if err := st.AppendMessage(id, llm.Message{Role: llm.RoleUser, Content: fmt.Sprintf("prompt %d", i)}); err != nil {
			t.Fatalf("append: %v", err)
		}
		ids[i] = id
	}
	return root, ids
}

func TestRunsPage(t *testing.T) {
	root, ids := fixtureRuns(t, 10)
	md, index, total, err := render.RunsPage(root, 1, 4)
	if err != nil {
		t.Fatalf("RunsPage: %v", err)
	}
	if total != 3 {
		t.Errorf("want 3 pages of 4 over 10 runs, got %d", total)
	}
	for _, want := range []string{"/run_1", "/run_4", "prompt 9", "page 1/3", "/runs 2"} {
		if !strings.Contains(md, want) {
			t.Errorf("page 1 missing %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, "/run_5") {
		t.Errorf("page 1 leaked a page-2 entry:\n%s", md)
	}
	// The index always covers the FULL listing, newest first.
	if len(index) != 10 {
		t.Fatalf("want a 10-entry index, got %d", len(index))
	}
	if index[1] != ids[9] {
		t.Errorf("index 1 should be the newest run %s, got %s", ids[9], index[1])
	}
	if index[10] != ids[0] {
		t.Errorf("index 10 should be the oldest run %s, got %s", ids[0], index[10])
	}

	md2, _, _, err := render.RunsPage(root, 2, 4)
	if err != nil {
		t.Fatalf("RunsPage page 2: %v", err)
	}
	for _, want := range []string{"/run_5", "/run_8", "page 2/3"} {
		if !strings.Contains(md2, want) {
			t.Errorf("page 2 missing %q:\n%s", want, md2)
		}
	}
}

// Sessions that never stored a message (the hidden cron dispatch parent,
// crash leftovers) are invisible to the listing: no entry, no index number —
// there is nothing to replay in them.
func TestRunsPageSkipsEmptySessions(t *testing.T) {
	root, ids := fixtureRuns(t, 2)
	st, err := runs.Open(root)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if _, err := st.NewSession(runs.Meta{Workdir: "/w", Model: "m"}); err != nil {
		t.Fatalf("new session: %v", err)
	}

	md, index, total, err := render.RunsPage(root, 1, 8)
	if err != nil {
		t.Fatalf("RunsPage: %v", err)
	}
	if total != 1 || len(index) != 2 {
		t.Fatalf("want the empty session skipped (1 page, 2 indexed), got total=%d index=%d", total, len(index))
	}
	if strings.Contains(md, "/run_3") {
		t.Fatalf("empty session leaked into the listing:\n%s", md)
	}
	if index[1] != ids[1] {
		t.Errorf("index 1 should be the newest NON-empty run %s, got %s", ids[1], index[1])
	}
}

func TestRunsPagePastEnd(t *testing.T) {
	root, _ := fixtureRuns(t, 3)
	md, _, total, err := render.RunsPage(root, 4, 4)
	if err != nil {
		t.Fatalf("RunsPage: %v", err)
	}
	if md != "" || total != 1 {
		t.Errorf("past-end page: want md==\"\" total==1, got total=%d md:\n%s", total, md)
	}
}

func TestRunsPageEmpty(t *testing.T) {
	md, _, total, err := render.RunsPage(t.TempDir(), 1, 8)
	if err != nil {
		t.Fatalf("RunsPage: %v", err)
	}
	if !strings.Contains(strings.ToLower(md), "no runs") {
		t.Errorf("expected an empty-state line:\n%s", md)
	}
	if total != 1 {
		t.Errorf("empty store should report 1 page, got %d", total)
	}
}

func TestRunsPageInvalid(t *testing.T) {
	if _, _, _, err := render.RunsPage(t.TempDir(), 0, 8); err == nil {
		t.Error("page 0: expected an error")
	}
	if _, _, _, err := render.RunsPage(t.TempDir(), 1, 0); err == nil {
		t.Error("size 0: expected an error")
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
	} {
		if !strings.Contains(out, want) {
			t.Errorf("Status missing %q\n---\n%s", want, out)
		}
	}
	if !strings.Contains(strings.ToLower(out), "armed") {
		t.Errorf("Status missing the gate line:\n%s", out)
	}
	// The system prompt is deliberately absent: it is thousands of tokens the
	// operator already has in shell3.sh, and including it pushed every /status
	// past Telegram's message cap into a document — where the tappable /job_N
	// commands stop being tappable.
	if strings.Contains(out, "You are shell3.") {
		t.Errorf("Status dumps the system prompt:\n%s", out)
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

// Cron/CronBrief rendering (from []cron.JobStatus) is covered by
// internal/render/cron_test.go, alongside the internal/cron package it
// depends on (unix-only).
