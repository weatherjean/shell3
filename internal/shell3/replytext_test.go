package shell3

import (
	"errors"
	"testing"
)

func TestReloadReplyText(t *testing.T) {
	if got := ReloadReplyText(ReloadResult{}, errors.New("boom")); got != "❌ reload failed: boom" {
		t.Errorf("error: %q", got)
	}
	got := ReloadReplyText(ReloadResult{Agents: 1, Models: 2, Jobs: 3}, nil)
	if got != "✅ reloaded — 1 agents, 2 models, 3 jobs" {
		t.Errorf("plain: %q", got)
	}
	got = ReloadReplyText(ReloadResult{Agents: 1, Notes: []string{"a", "b"}}, nil)
	if want := "✅ reloaded — 1 agents, 0 models, 0 jobs\n• a\n• b"; got != want {
		t.Errorf("notes: %q, want %q", got, want)
	}
}
