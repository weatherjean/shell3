package shell3

import (
	"errors"
	"strings"
	"testing"
)

func TestSettableListText(t *testing.T) {
	if got := SettableListText(nil); got != "no settable parameters for this model" {
		t.Errorf("empty: %q", got)
	}
	params := []ParamValue{
		{Name: "reasoning_effort", Value: "", Default: "medium", Enum: []string{"low", "medium", "high"}},
		{Name: "temperature", Value: "0.7"},
		{Name: "max_tokens", Value: "", Default: ""},
	}
	got := SettableListText(params)
	for _, want := range []string{
		"⚙️ settable parameters — /set <name> <value>:\n",
		"• reasoning_effort = medium (default) [low | medium | high]\n",
		"• temperature = 0.7\n",
		"• max_tokens = unset\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestParseSetArgs(t *testing.T) {
	tests := []struct {
		in          string
		name, value string
		ok          bool
	}{
		{"temperature 0.7", "temperature", "0.7", true},
		{"temperature   two words", "temperature", "two words", true},
		{"temperature\t0.5", "temperature", "0.5", true},
		{"temperature", "", "", false},
		{"", "", "", false},
		{"   ", "", "", false},
	}
	for _, tc := range tests {
		name, value, ok := ParseSetArgs(tc.in)
		if name != tc.name || value != tc.value || ok != tc.ok {
			t.Errorf("ParseSetArgs(%q) = (%q, %q, %v), want (%q, %q, %v)",
				tc.in, name, value, ok, tc.name, tc.value, tc.ok)
		}
	}
}

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
