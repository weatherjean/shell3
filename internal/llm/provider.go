package llm

import "context"

// Streamer is the streaming surface every LLM client exposes.
type Streamer interface {
	Stream(ctx context.Context, msgs []Message, tools []ToolDefinition, onEvent func(StreamEvent)) error
}

// TrafficInspector is implemented by Streamers that buffer the last raw
// HTTP request/response they handled.
type TrafficInspector interface {
	LastTraffic() (req, res []byte)
}
