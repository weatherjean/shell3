package chat

import (
	"encoding/json"
	"time"
)

// MarshalEventJSON serializes a chat Event as the canonical JSON object shared
// by the --out JSONL sink and the web event stream. "ts" is the event's own
// timestamp (RFC3339Nano, UTC). Empty fields are omitted.
func MarshalEventJSON(ev Event) ([]byte, error) {
	rec := map[string]any{
		"ts":   ev.Time.UTC().Format(time.RFC3339Nano),
		"kind": ev.Kind.String(),
	}
	if ev.SessionID != 0 {
		rec["session_id"] = ev.SessionID
	}
	if ev.Text != "" {
		rec["text"] = ev.Text
	}
	if ev.Role != "" {
		rec["role"] = ev.Role
	}
	if ev.ToolName != "" {
		rec["tool"] = ev.ToolName
	}
	if ev.ToolInput != "" {
		rec["input"] = ev.ToolInput
	}
	if ev.ToolOutput != "" {
		rec["output"] = ev.ToolOutput
	}
	if ev.ToolCallID != "" {
		rec["call_id"] = ev.ToolCallID
	}
	if ev.ToolError {
		rec["tool_error"] = true
	}
	if ev.Usage != nil {
		rec["usage"] = map[string]int{
			"prompt":     ev.Usage.PromptTokens,
			"completion": ev.Usage.CompletionTokens,
			"total":      ev.Usage.TotalTokens,
		}
	}
	if len(ev.Meta) > 0 {
		rec["meta"] = ev.Meta
	}
	return json.Marshal(rec)
}
