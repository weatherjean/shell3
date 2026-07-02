package acp

import (
	"encoding/json"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/weatherjean/shell3/pkg/shell3"
)

// replayUpdates converts stored session history into a sequence of ACP
// SessionUpdate values that can be replayed to a client loading the session.
//
// The mapping mirrors the live streaming path (session.go / updatesForEvent):
//
//   - user role       → UpdateUserMessageText(Content)
//   - assistant Reasoning (non-empty) → UpdateAgentThoughtText (thought chunk)
//   - assistant Content (non-empty)   → UpdateAgentMessageText (message chunk)
//   - assistant ToolCalls → StartToolCall per invocation (kind from toolKind,
//     raw input from the stored JSON args)
//   - tool role       → UpdateToolCall (Completed, content from Content)
//   - system          → skipped (host-managed; not replayed to the client)
//
// This is a pure function with no I/O; it is tested directly in sessions_test.go.
func replayUpdates(entries []shell3.HistoryEntry) []acpsdk.SessionUpdate {
	var out []acpsdk.SessionUpdate
	for _, e := range entries {
		switch e.Role {
		case "user":
			if e.Content != "" {
				out = append(out, acpsdk.UpdateUserMessageText(e.Content))
			}

		case "assistant":
			if e.Reasoning != "" {
				out = append(out, acpsdk.UpdateAgentThoughtText(e.Reasoning))
			}
			if e.Content != "" {
				out = append(out, acpsdk.UpdateAgentMessageText(e.Content))
			}
			for _, tc := range e.ToolCalls {
				out = append(out, acpsdk.StartToolCall(
					acpsdk.ToolCallId(tc.ID),
					tc.Name,
					acpsdk.WithStartKind(toolKind(tc.Name)),
					acpsdk.WithStartRawInput(json.RawMessage(tc.Args)),
				))
			}

		case "tool":
			out = append(out, acpsdk.UpdateToolCall(
				acpsdk.ToolCallId(e.ToolCallID),
				acpsdk.WithUpdateStatus(acpsdk.ToolCallStatusCompleted),
				acpsdk.WithUpdateContent([]acpsdk.ToolCallContent{
					acpsdk.ToolContent(acpsdk.TextBlock(e.Content)),
				}),
			))

			// "system": skip — host-managed reminders are not replayed.
		}
	}
	return out
}
