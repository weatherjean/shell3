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

// ContentPartType identifies the kind of content in a ContentPart.
type ContentPartType string

const (
	ContentPartTypeText     ContentPartType = "text"
	ContentPartTypeImageURL ContentPartType = "image_url" // data URI or HTTPS URL
)

// ContentPart is one element of a multimodal user message.
type ContentPart struct {
	Type     ContentPartType
	Text     string
	ImageURL string // data URI ("data:image/jpeg;base64,...") or HTTPS URL
}

// Message is one turn in a conversation.
type Message struct {
	Role       Role       `json:"role"`
	Content    string     `json:"content"`
	// ContentParts replaces Content for multimodal messages (vision).
	ContentParts []ContentPart `json:"content_parts,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	// ReasoningContent holds the model's chain-of-thought when the
	// provider exposes one (Moonshot/kimi, DeepSeek). Required to be
	// echoed back on assistant tool-call messages by Moonshot when
	// thinking mode is enabled, otherwise the next request 400s.
	ReasoningContent string `json:"reasoning_content,omitempty"`
	// ProviderReasoning is opaque JSON an adapter needs echoed back on the
	// next turn (e.g. codex `reasoning` items with `encrypted_content`).
	// Chat layer treats it as a black box; only the originating adapter parses.
	ProviderReasoning []byte `json:"provider_reasoning,omitempty"`
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

// Usage holds token counts for a completed turn.
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// StreamEvent is one event from the LLM stream.
type StreamEvent struct {
	TextDelta      string
	ReasoningDelta string
	ToolCall       *ToolCall
	Usage          *Usage
	Done           bool
	// ProviderReasoning is opaque JSON the adapter wants attached to the
	// assistant message it is currently streaming. Chat layer copies it
	// onto the assistant Message verbatim.
	ProviderReasoning []byte
}
