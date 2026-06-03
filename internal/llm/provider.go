package llm

import "context"

// Streamer is the streaming surface every LLM client exposes.
// onEvent is always invoked from exactly one goroutine — the goroutine that
// called Stream — so callers may mutate closed-over state without additional
// synchronization.
type Streamer interface {
	Stream(ctx context.Context, msgs []Message, tools []ToolDefinition, onEvent func(StreamEvent)) error
}

// TrafficInspector is implemented by Streamers that buffer the last raw
// HTTP request/response they handled.
type TrafficInspector interface {
	LastTraffic() (req, res []byte)
}
