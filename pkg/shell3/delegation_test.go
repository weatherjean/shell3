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
		ParentSession: "42",
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

// TestRenderDelegation_InboxAbsolutePaths asserts that when RunsDir is known the
// spawn command targets ABSOLUTE paths under the parent's runtime root: --out to
// the parent's agents dir and --inbox to the parent's inbox.jsonl. This is what
// lets a subagent (which runs from its own cwd) report to the file THIS host
// watches, rather than to its own incidental project inbox.
func TestRenderDelegation_InboxAbsolutePaths(t *testing.T) {
	out := renderDelegation(delegationParams{
		Binary:        "shell3",
		ConfigPath:    "/c/shell3.lua",
		WorkDir:       "/wd",
		RunsDir:       "/root/.shell3_project/runs",
		ParentSession: "42",
		Subagents:     []subagentItem{{Name: "explore", Description: "search"}},
	})
	if !strings.Contains(out, "--inbox /root/.shell3_project/inbox.jsonl") {
		t.Errorf("expected absolute --inbox under parent root:\n%s", out)
	}
	if !strings.Contains(out, "--out /root/.shell3_project/agents/<id>.jsonl") {
		t.Errorf("expected absolute --out under parent agents dir:\n%s", out)
	}
}

// TestRenderDelegation_NoInboxWithoutRunsDir asserts the fallback: with no
// RunsDir the command stays relative and omits --inbox (no regression for
// callers that cannot resolve a runtime root).
func TestRenderDelegation_NoInboxWithoutRunsDir(t *testing.T) {
	out := renderDelegation(delegationParams{
		Binary:        "shell3",
		ConfigPath:    "/c/shell3.lua",
		ParentSession: "42",
		Subagents:     []subagentItem{{Name: "explore"}},
	})
	if strings.Contains(out, "--inbox") {
		t.Errorf("did not expect --inbox without RunsDir:\n%s", out)
	}
	if !strings.Contains(out, "--out .shell3_project/agents/<id>.jsonl") {
		t.Errorf("expected relative --out fallback:\n%s", out)
	}
}

// TestRenderDelegation_EmptyWhenNoSubagents asserts the section is omitted when
// there is nothing to delegate to.
func TestRenderDelegation_EmptyWhenNoSubagents(t *testing.T) {
	if got := renderDelegation(delegationParams{Binary: "shell3"}); got != "" {
		t.Errorf("renderDelegation with no subagents = %q, want empty", got)
	}
}
