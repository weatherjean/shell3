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

func TestStopReplyText(t *testing.T) {
	tests := []struct {
		cancelled bool
		killed    int
		want      string
	}{
		{true, 2, "⏹ stopped — killed 2 background job(s)"},
		{true, 0, "⏹ stopped"},
		{false, 3, "⏹ no turn running — killed 3 background job(s)"},
		{false, 0, "nothing running"},
	}
	for _, tc := range tests {
		if got := StopReplyText(tc.cancelled, tc.killed); got != tc.want {
			t.Errorf("StopReplyText(%v, %d) = %q, want %q", tc.cancelled, tc.killed, got, tc.want)
		}
	}
}
