package shell3

import (
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/notify"
)

// TestRenderNotification_AgentDone verifies the agent_done branch injects the
// subagent's result summary (CAPPED so a huge final message can't blow up the
// parent's context), tells the model to RELAY it, and points at task_status
// (by job id) for the full result when the summary is truncated or empty.
func TestRenderNotification_AgentDone(t *testing.T) {
	got := renderNotification(notify.Notification{
		Kind: "agent_done", ID: "sub1", Status: "ok",
		Preview: "Found 3 call sites in pkg/foo.",
	})
	for _, want := range []string{
		"sub1", "Found 3 call sites",
		"relay it to the user", // the agent must surface the result, not sit on it
	} {
		if !strings.Contains(got, want) {
			t.Errorf("agent_done notice %q missing %q", got, want)
		}
	}
	// The old wording told the agent it was already done ("act on it directly;
	// you do NOT need to read anything else") — guard against that regressing.
	if strings.Contains(got, "you do NOT need to read anything else") {
		t.Errorf("agent_done notice still discourages relaying: %q", got)
	}

	// A super-long summary is capped and points at task_status for the rest, so
	// it cannot blow up the parent's context.
	long := strings.Repeat("x", agentDoneResultCap+500)
	trunc := renderNotification(notify.Notification{Kind: "agent_done", ID: "sub2", Preview: long})
	if !strings.Contains(trunc, "truncated") || !strings.Contains(trunc, "task_status sub2") {
		t.Errorf("long-summary agent_done should truncate + point to task_status; got %d runes", len([]rune(trunc)))
	}
	if len([]rune(trunc)) >= len([]rune(long)) {
		t.Errorf("long-summary agent_done not capped: %d runes >= raw %d", len([]rune(trunc)), len([]rune(long)))
	}

	// With no preview, point at task_status (by job id) and still say to relay.
	noPrev := renderNotification(notify.Notification{Kind: "agent_done", ID: "x", Status: "ok"})
	if !strings.Contains(noPrev, "task_status x") || !strings.Contains(noPrev, "relay it to the user") {
		t.Errorf("preview-less agent_done = %q, want task_status pointer + relay instruction", noPrev)
	}

	// A status-less notification still renders a sane default.
	if g := renderNotification(notify.Notification{Kind: "agent_done", ID: "x"}); !strings.Contains(g, "subagent x finished (done)") {
		t.Errorf("status-less agent_done = %q, want default 'done' status", g)
	}
}
