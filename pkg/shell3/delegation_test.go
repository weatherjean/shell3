package shell3

import (
	"strings"
	"testing"
)

// TestRenderDelegation_TaskTool asserts the rendered Delegation section lists
// the allowed subagents and tells the model to use the `task` tool.
func TestRenderDelegation_TaskTool(t *testing.T) {
	out := renderDelegation(delegationParams{
		Subagents: []subagentItem{{Name: "explore", Description: "search"}},
	})
	if !strings.Contains(out, "`task`") {
		t.Errorf("expected `task` tool reference:\n%s", out)
	}
	if !strings.Contains(out, "subagent_type") {
		t.Errorf("expected subagent_type param:\n%s", out)
	}
	if !strings.Contains(out, "- explore: search") {
		t.Errorf("expected subagent listing:\n%s", out)
	}
	// Must NOT mention the old bash_bg command pattern.
	if strings.Contains(out, "shell3 run ") {
		t.Errorf("old shell3 run command must be gone:\n%s", out)
	}
	if strings.Contains(out, "--parent-session") {
		t.Errorf("old --parent-session flag must be gone:\n%s", out)
	}
	// Must mention the three management tools.
	for _, want := range []string{"task_list", "task_status", "task_cancel"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected %q in delegation reminder:\n%s", want, out)
		}
	}
	// Must NOT reference .shell3_project/runs/jobs paths.
	if strings.Contains(out, ".shell3_project/runs/jobs") {
		t.Errorf("delegation reminder must not reference run/jobs paths:\n%s", out)
	}
}

// TestRenderDelegation_EmptyWhenNoSubagents asserts the section is omitted when
// there is nothing to delegate to.
func TestRenderDelegation_EmptyWhenNoSubagents(t *testing.T) {
	if got := renderDelegation(delegationParams{}); got != "" {
		t.Errorf("renderDelegation with no subagents = %q, want empty", got)
	}
}
