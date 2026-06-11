package shell3

import (
	"strings"
	"testing"
)

// TestRenderDelegation_TemplatesSpawnCommand asserts the rendered Delegation
// section lists the allowed subagents and embeds the exact bash_bg spawn command
// with every runtime path substituted and the depth/notify flags set.
func TestRenderDelegation_TemplatesSpawnCommand(t *testing.T) {
	got := renderDelegation(delegationParams{
		Binary:     "/usr/local/bin/shell3",
		ConfigPath: "/home/me/.shell3/shell3.lua",
		SinkPath:   "/proj/.shell3/sink/main.jsonl",
		WorkDir:    "/proj",
		Subagents: []subagentItem{
			{Name: "explorer", Description: "read-only codebase exploration"},
			{Name: "bare"}, // no description still lists the name
		},
	})
	for _, want := range []string{
		"## Delegation",
		"- explorer: read-only codebase exploration",
		"- bare\n",
		"/usr/local/bin/shell3 --config /home/me/.shell3/shell3.lua",
		"--agent <name>",
		"--out .shell3/agents/<id>.jsonl",
		"--append-sinkfile /proj/.shell3/sink/main.jsonl",
		"--id <id>",
		"--no-subagents",
		"notify_on_exit=false",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("delegation section missing %q\n---\n%s", want, got)
		}
	}
}

// TestRenderDelegation_EmptyWhenNoSubagents asserts the section is omitted when
// there is nothing to delegate to.
func TestRenderDelegation_EmptyWhenNoSubagents(t *testing.T) {
	if got := renderDelegation(delegationParams{Binary: "shell3"}); got != "" {
		t.Errorf("renderDelegation with no subagents = %q, want empty", got)
	}
}

// TestApplyDelegationContext_Idempotent asserts applyDelegationContext strips a
// prior Delegation section before re-appending, so repeated application (agent
// switch / reload / clear) never duplicates it. It drives the strip/replace path
// directly against a synthesized prompt via stripDelegation.
func TestApplyDelegationContext_Idempotent(t *testing.T) {
	base := "you are an agent.\n## Environment\n- history_db: /x"
	section := renderDelegation(delegationParams{
		Binary: "shell3", ConfigPath: "/c.lua", SinkPath: "/s.jsonl", WorkDir: "/w",
		Subagents: []subagentItem{{Name: "explorer", Description: "explore"}},
	})
	withOnce := base + section
	if n := strings.Count(withOnce, delegationMarker); n != 1 {
		t.Fatalf("expected 1 delegation marker, got %d", n)
	}
	// Re-deriving from withOnce (e.g. a reload that carried the appended prompt)
	// must strip the old section first so a re-append leaves exactly one.
	stripped := stripDelegation(withOnce)
	if stripped != base {
		t.Fatalf("stripDelegation = %q, want %q", stripped, base)
	}
	reAppended := stripped + section
	if n := strings.Count(reAppended, delegationMarker); n != 1 {
		t.Fatalf("after strip+reapply: %d markers, want 1", n)
	}
}

// TestApplyDelegationContext_SuppressedNoSubagents asserts a session started with
// --no-subagents (DisableSubagents) gets no Delegation section even when its
// agent has an allowlist — enforcing depth limit 1.
func TestApplyDelegationContext_SuppressedNoSubagents(t *testing.T) {
	s := &Session{}
	s.opts.DisableSubagents = true
	s.cfg.Subagents = []string{"explorer"}
	s.cfg.WorkDir = t.TempDir()
	s.name = "child"
	s.cfg.Personality.SystemPrompt = "base prompt"
	s.applyDelegationContext(&Runtime{}) // rt fields irrelevant; DisableSubagents short-circuits
	if strings.Contains(s.cfg.Personality.SystemPrompt, "## Delegation") {
		t.Errorf("child with --no-subagents must get no delegation context, got: %q", s.cfg.Personality.SystemPrompt)
	}
}
