package chat

import (
	"context"
	"encoding/json"
)

// funcHandler adapts a closure to ToolHandler for turn-loop tests.
type funcHandler struct {
	name string
	fn   func(context.Context, string, json.RawMessage, ToolConfig) (string, error)
}

func (h funcHandler) Name() string { return h.name }

func (h funcHandler) Execute(ctx context.Context, id string, args json.RawMessage, cfg ToolConfig) (string, error) {
	return h.fn(ctx, id, args, cfg)
}
