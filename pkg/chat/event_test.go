package chat

import "testing"

func TestEventKindString(t *testing.T) {
	cases := []struct {
		kind EventKind
		want string
	}{
		{EventSessionStart, "session_start"},
		{EventSessionEnd, "session_end"},
		{EventUserMessage, "user_message"},
		{EventAssistantToken, "assistant_token"},
		{EventAssistantMessage, "assistant_message"},
		{EventToolCall, "tool_call"},
		{EventToolResult, "tool_result"},
		{EventError, "error"},
		{EventUsage, "usage"},
		{EventAssistantReasoning, "assistant_reasoning"},
		{EventTurnDone, "turn_done"},
		{EventSystemReminder, "system_reminder"},
	}
	for _, c := range cases {
		if got := c.kind.String(); got != c.want {
			t.Errorf("EventKind(%d).String() = %q, want %q", c.kind, got, c.want)
		}
	}
}
