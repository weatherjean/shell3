package chat

import (
	"bytes"
	"strings"
	"testing"
	"time"

)

func TestSinkWriteChatEventToolCall(t *testing.T) {
	var buf bytes.Buffer
	s := newOutSink(&buf, time.Unix(1700000000, 0).UTC())
	s.WriteChatEvent(Event{
		Kind:       EventToolCall,
		Time:       time.Unix(1700000000, 0).UTC(),
		ToolName:   "bash",
		ToolInput:  `{"cmd":"ls"}`,
		ToolCallID: "c1",
	})
	got := buf.String()
	if !strings.Contains(got, `"kind":"tool_call"`) {
		t.Errorf("missing kind: %s", got)
	}
	if !strings.Contains(got, `"tool":"bash"`) {
		t.Errorf("missing tool: %s", got)
	}
	if !strings.Contains(got, `"call_id":"c1"`) {
		t.Errorf("missing call_id: %s", got)
	}
}

func TestSinkWriteChatEventUsage(t *testing.T) {
	var buf bytes.Buffer
	s := newOutSink(&buf, time.Unix(1700000000, 0).UTC())
	s.WriteChatEvent(Event{
		Kind:  EventUsage,
		Time:  time.Unix(1700000000, 0).UTC(),
		Usage: &EventUsageData{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3},
	})
	got := buf.String()
	if !strings.Contains(got, `"kind":"usage"`) {
		t.Errorf("missing kind: %s", got)
	}
	if !strings.Contains(got, `"total":3`) {
		t.Errorf("missing usage total: %s", got)
	}
}

func TestSink_StartEndBracket(t *testing.T) {
	var buf bytes.Buffer
	s := newOutSink(&buf, time.Unix(0, 0).UTC())
	s.WriteStart("hi", "default", "gpt-5", "/tmp/out", true)
	s.WriteEnd("ok")
	out := buf.String()
	if !strings.Contains(out, `"kind":"start"`) || !strings.Contains(out, `"input":"hi"`) {
		t.Fatalf("missing start: %q", out)
	}
	if !strings.Contains(out, `"kind":"end"`) || !strings.Contains(out, `"status":"ok"`) {
		t.Fatalf("missing end: %q", out)
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
}
