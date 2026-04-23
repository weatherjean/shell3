// Package output defines event types and emitter implementations for agent output.
package output

// EventType identifies the kind of output event.
type EventType string

const (
	EventThinking   EventType = "thinking"
	EventToken      EventType = "token"
	EventToolCall   EventType = "tool_call"
	EventToolResult EventType = "tool_result"
	EventDone       EventType = "done"
	EventError      EventType = "error"
)

// Event carries one unit of agent output.
type Event struct {
	Type     EventType      `json:"type"`
	Text     string         `json:"text,omitempty"`
	Tool     string         `json:"tool,omitempty"`
	Params   map[string]any `json:"params,omitempty"`
	ExitCode int            `json:"exit_code,omitempty"`
	Message  string         `json:"message,omitempty"`
}
