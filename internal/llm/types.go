// Package llm provides an OpenAI-compatible streaming LLM client.
package llm

// Role identifies the author of a conversation message.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message is one turn in a conversation.
type Message struct {
	Role       Role   `json:"role"`
	Content    string `json:"content"`
	ToolCallID string `json:"tool_call_id,omitempty"`
	Name       string `json:"name,omitempty"`
}

// ToolDefinition describes a tool the LLM may call.
type ToolDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// ToolCall holds a single tool invocation returned by the LLM.
type ToolCall struct {
	ID      string
	Name    string
	RawArgs string
}

// StreamEvent is one event from the LLM stream.
type StreamEvent struct {
	TextDelta string
	ToolCall  *ToolCall
	Done      bool
}
