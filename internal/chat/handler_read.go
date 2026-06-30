package chat

import (
	"context"
	"encoding/json"
)

// ReadHandler implements the read built-in tool. It delegates to handleReadTool.
type ReadHandler struct{}

func (ReadHandler) Name() string { return "read" }

func (ReadHandler) Execute(_ context.Context, _ string, args json.RawMessage, cfg ToolConfig) (string, error) {
	return handleReadTool(string(args), cfg.WorkDir), nil
}
