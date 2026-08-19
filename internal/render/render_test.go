package render_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

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

// fixtureRuns writes n one-message sessions and returns (root, ids) in
// creation order (ListSessions returns newest first).
func fixtureRuns(t *testing.T, n int) (string, []string) {
	t.Helper()
	root := t.TempDir()
	st, err := runs.Open(root)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	ids := make([]string, n)
	for i := 0; i < n; i++ {
		id, err := st.NewSession(runs.Meta{})
		if err != nil {
			t.Fatalf("new session %d: %v", i, err)
		}
		if err := st.AppendMessage(id, llm.Message{Role: llm.RoleUser, Content: fmt.Sprintf("prompt %d", i)}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
		ids[i] = id
	}
	return root, ids
}

// The dash index replaces /status, so a live session's snapshot must surface
// the same facts /status carried: model, tools, employees, skills, warnings,
// the gate — and never the system prompt.
func TestDashIndexHTMLLiveSession(t *testing.T) {
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

	out := render.DashIndexHTML(sess, rt, "0.2.0", nil, nil, nil, "")
	for _, want := range []string{
		"kimi-k2-0905-preview",
		"0.2.0",
		"bash",
		"explorer",
		"scripting",
		"skill x skipped",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("index missing %q\n---\n%s", want, out)
		}
	}
	if !strings.Contains(strings.ToLower(out), "armed") {
		t.Errorf("index missing the gate line:\n%s", out)
	}
	// The system prompt is deliberately absent: it is thousands of tokens the
	// operator already has in shell3.sh.
	if strings.Contains(out, "You are shell3.") {
		t.Errorf("index dumps the system prompt:\n%s", out)
	}
}

func TestRunsPageHTMLPagination(t *testing.T) {
	root, ids := fixtureRuns(t, 10)
	frag, total, err := render.RunsPageHTML(root, 1, 4, "tk")
	if err != nil {
		t.Fatalf("RunsPageHTML: %v", err)
	}
	if total != 3 {
		t.Errorf("want 3 pages of 4 over 10 runs, got %d", total)
	}
	// Newest first: page 1 carries the last-created runs.
	if !strings.Contains(frag, "prompt 9") || strings.Contains(frag, "prompt 0") {
		t.Errorf("page 1 should hold the newest runs:\n%s", frag)
	}
	if !strings.Contains(frag, "/runs/"+ids[9]+"?t=tk") {
		t.Errorf("newest run link missing:\n%s", frag)
	}
	if !strings.Contains(frag, "/runs?page=2&amp;t=tk") {
		t.Errorf("older-page link missing:\n%s", frag)
	}
	frag2, _, err := render.RunsPageHTML(root, 3, 4, "tk")
	if err != nil {
		t.Fatalf("page 3: %v", err)
	}
	if !strings.Contains(frag2, "prompt 0") {
		t.Errorf("last page should hold the oldest run:\n%s", frag2)
	}
}

// Message-less sessions (dispatch parents, crash leftovers) are invisible in
// the listing — nothing to replay.
func TestRunsPageHTMLSkipsEmptySessions(t *testing.T) {
	root, _ := fixtureRuns(t, 2)
	st, err := runs.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.NewSession(runs.Meta{}); err != nil { // empty: no messages
		t.Fatal(err)
	}
	st.Close()
	frag, total, err := render.RunsPageHTML(root, 1, 8, "tk")
	if err != nil {
		t.Fatalf("RunsPageHTML: %v", err)
	}
	if total != 1 {
		t.Errorf("total = %d, want 1 page", total)
	}
	if got := strings.Count(frag, "<tr><td><a href="); got != 2 {
		t.Errorf("listing rows = %d, want 2 (empty session hidden):\n%s", got, frag)
	}
}
