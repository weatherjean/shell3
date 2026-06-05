package chat

import "testing"

func TestEmitToolCallAndResult(t *testing.T) {
	s, c := newCollectorSession(SessionOpts{})
	s.id = 7
	emitToolCall(s, "call_1", "bash", `{"cmd":"ls"}`)
	emitToolResult(s, "call_1", "bash", "file1\nfile2\n", false)

	got := c.all()
	if len(got) != 2 {
		t.Fatalf("got %d events, want 2", len(got))
	}
	if got[0].Kind != EventToolCall || got[0].ToolName != "bash" || got[0].ToolCallID != "call_1" {
		t.Errorf("tool_call event mismatch: %+v", got[0])
	}
	if got[0].ToolInput != `{"cmd":"ls"}` {
		t.Errorf("tool_call input = %q", got[0].ToolInput)
	}
	if got[1].Kind != EventToolResult || got[1].ToolOutput != "file1\nfile2\n" || got[1].ToolError {
		t.Errorf("tool_result event mismatch: %+v", got[1])
	}
}
