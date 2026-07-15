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

// ContentPartType identifies the kind of content in a ContentPart.
type ContentPartType string

const (
	ContentPartTypeText       ContentPartType = "text"
	ContentPartTypeImageURL   ContentPartType = "image_url"   // data URI or HTTPS URL
	ContentPartTypeInputAudio ContentPartType = "input_audio" // base64 wav/mp3
)

// ContentPart is one element of a multimodal user message.
type ContentPart struct {
	Type        ContentPartType
	Text        string
	ImageURL    string // data URI ("data:image/jpeg;base64,...") or HTTPS URL
	AudioData   string // base64-encoded raw audio bytes (input_audio)
	AudioFormat string // "wav" | "mp3" | "ogg"
}

// Message is one turn in a conversation.
type Message struct {
	Role    Role   `json:"role"`
	Content string `json:"content"`
	// ContentParts replaces Content for multimodal messages (vision).
	ContentParts []ContentPart `json:"content_parts,omitempty"`
	ToolCallID   string        `json:"tool_call_id,omitempty"`
	Name         string        `json:"name,omitempty"`
	ToolCalls    []ToolCall    `json:"tool_calls,omitempty"`
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
}
