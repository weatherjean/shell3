package chat

import (
	"encoding/json"
	"testing"
	"time"
)

func TestMarshalEventJSON_Fields(t *testing.T) {
	ev := Event{
		Kind:       EventToolResult,
		Time:       time.Date(2026, 6, 2, 12, 0, 0, 0, time.UTC),
		SessionID:  7,
		ToolName:   "bash",
		ToolOutput: "ok",
		ToolCallID: "3",
		ToolError:  true,
	}
	b, err := MarshalEventJSON(ev)
	if err != nil {
		t.Fatalf("MarshalEventJSON: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["kind"] != "tool_result" {
		t.Errorf("kind = %v, want tool_result", got["kind"])
	}
	if got["tool"] != "bash" || got["output"] != "ok" || got["call_id"] != "3" {
		t.Errorf("tool fields wrong: %v", got)
	}
	if got["tool_error"] != true {
		t.Errorf("tool_error = %v, want true", got["tool_error"])
	}
	if got["ts"] != "2026-06-02T12:00:00Z" {
		t.Errorf("ts = %v", got["ts"])
	}
}

func TestMarshalEventJSON_OmitsEmpty(t *testing.T) {
	b, err := MarshalEventJSON(Event{Kind: EventTurnDone, Time: time.Unix(0, 0).UTC()})
	if err != nil {
		t.Fatalf("MarshalEventJSON: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["kind"] != "turn_done" {
		t.Errorf("kind = %v, want turn_done", got["kind"])
	}
	for _, k := range []string{"text", "tool", "session_id", "usage", "meta"} {
		if _, ok := got[k]; ok {
			t.Errorf("expected %q omitted, got %v", k, got[k])
		}
	}
}
