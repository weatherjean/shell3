package shell3

import (
	"strings"
	"testing"
)

// TestRenderDelegation_NewTemplate asserts the rendered Delegation section lists
// the allowed subagents and embeds the new `shell3 run` spawn command with the
// --parent-session report pointer, and that the retired flags are gone.
func TestRenderDelegation_NewTemplate(t *testing.T) {
	out := renderDelegation(delegationParams{
		Binary:        "shell3",
		ConfigPath:    "/c/shell3.lua",
		WorkDir:       "/wd",
		ParentSession: 42,
		Subagents:     []subagentItem{{Name: "explore", Description: "search"}},
	})
	if !strings.Contains(out, "shell3 run ") {
		t.Errorf("expected `shell3 run` subcommand:\n%s", out)
	}
	if !strings.Contains(out, "--parent-session 42") {
		t.Errorf("expected --parent-session 42:\n%s", out)
	}
	if !strings.Contains(out, "--agent <name>") || !strings.Contains(out, "--prompt \"<task>\"") {
		t.Errorf("expected new template flags:\n%s", out)
	}
	if strings.Contains(out, "--append-sinkfile") || strings.Contains(out, "--no-subagents") {
		t.Errorf("retired flags must be gone:\n%s", out)
	}
	if !strings.Contains(out, "- explore: search") {
		t.Errorf("expected subagent listing:\n%s", out)
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
		Binary: "shell3", ConfigPath: "/c.lua", ParentSession: 7, WorkDir: "/w",
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
