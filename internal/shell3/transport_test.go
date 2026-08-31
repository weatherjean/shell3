package shell3

import (
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/notify"
)

func TestRenderNotification_AgentDone(t *testing.T) {
	got := renderNotification(notify.Notification{
		Kind: "agent_done", ID: "sub1", Status: "ok",
		Preview: "Found 3 call sites in pkg/foo.",
	})
	for _, want := range []string{
		"sub1", "Found 3 call sites",
		"relay it to the user",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("agent_done notice %q missing %q", got, want)
		}
	}
	if strings.Contains(got, "you do NOT need to read anything else") {
		t.Errorf("agent_done notice still discourages relaying: %q", got)
	}

	long := strings.Repeat("x", agentDoneResultCap+500)
	trunc := renderNotification(notify.Notification{Kind: "agent_done", ID: "sub2", Preview: long})
	if !strings.Contains(trunc, "truncated") || !strings.Contains(trunc, "task_status sub2") {
		t.Errorf("long-summary agent_done should truncate + point to task_status; got %d runes", len([]rune(trunc)))
	}
	if len([]rune(trunc)) >= len([]rune(long)) {
		t.Errorf("long-summary agent_done not capped: %d runes >= raw %d", len([]rune(trunc)), len([]rune(long)))
	}

	noPrev := renderNotification(notify.Notification{Kind: "agent_done", ID: "x", Status: "ok"})
	if !strings.Contains(noPrev, "task_status x") || !strings.Contains(noPrev, "relay it to the user") {
		t.Errorf("preview-less agent_done = %q, want task_status pointer + relay instruction", noPrev)
	}

	if g := renderNotification(notify.Notification{Kind: "agent_done", ID: "x"}); !strings.Contains(g, "subagent x finished (done)") {
		t.Errorf("status-less agent_done = %q, want default 'done' status", g)
	}
}
