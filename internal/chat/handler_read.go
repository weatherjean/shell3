package chat

import (
	"context"
	"encoding/json"
)

// ReadHandler implements the read built-in tool. It delegates to handleReadTool.
type ReadHandler struct{}

func (ReadHandler) Name() string { return "read" }

func (ReadHandler) Execute(ctx context.Context, _ string, args json.RawMessage, cfg ToolConfig) (string, error) {
	return handleReadTool(ctx, string(args), cfg.WorkDir, cfg.fs()), nil
}
