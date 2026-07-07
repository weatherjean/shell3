package chat

import (
	"context"
	"encoding/json"
)

// EditHandler implements the edit_file built-in tool.
// It delegates to handleEditTool in edit_dispatch.go.
type EditHandler struct{}

func (EditHandler) Name() string { return "edit_file" }

func (EditHandler) Execute(ctx context.Context, id string, args json.RawMessage, cfg ToolConfig) (string, error) {
	return handleEditTool(ctx, string(args), cfg.WorkDir), nil
}
