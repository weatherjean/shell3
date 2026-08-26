// Package llm holds the vendor-neutral LLM types (messages, tool definitions,
// stream events, request params) and capability interfaces shared by the chat
// core and the provider adapters; the OpenAI-compatible client itself lives in
// internal/adapter/openai.
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
	Role       Role       `json:"role"`
	Content    string     `json:"content"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	// ReasoningContent holds the non-standard chain-of-thought text the openai
	// adapter populates from streaming and echoes back on the next turn:
	// Moonshot 400s when thinking mode is on and an assistant tool-call message
	// lacks reasoning_content.
	ReasoningContent string `json:"reasoning_content,omitempty"`
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
	// CachedTokens is how much of PromptTokens the provider served from its
	// prompt cache (usage.prompt_tokens_details.cached_tokens); 0 when the
	// provider doesn't report it.
	CachedTokens int
}

// RetryNotice describes a transient request failure that is about to be
// retried. Adapters emit one via StreamEvent.Retry so the retry — otherwise
// invisible inside the SDK's retry loop — can be surfaced to the user.
type RetryNotice struct {
	Attempt int    // 1-based index of the upcoming retry
	Max     int    // maximum number of retries that will be attempted
	Reason  string // why the attempt failed (e.g. "HTTP 503", "connection error: …")
}

// StreamEvent is one event from the LLM stream.
type StreamEvent struct {
	TextDelta      string
	ReasoningDelta string
	ToolCall       *ToolCall
	Usage          *Usage
	Retry          *RetryNotice
	Done           bool
	// Truncated reports that the provider cut the response at the output
	// token cap (finish_reason "length") rather than finishing it. Set on the
	// terminal event. Without it a capped reply is indistinguishable from a
	// complete one — it simply stops mid-sentence, with no error to point at.
	Truncated bool
}
