package chat

import (
	"context"
	"encoding/json"
)

// ListFilesHandler implements the list_files built-in tool. It delegates to
// handleListFilesTool.
type ListFilesHandler struct{}

func (ListFilesHandler) Name() string { return "list_files" }

func (ListFilesHandler) Execute(_ context.Context, _ string, args json.RawMessage, cfg ToolConfig) (string, error) {
	return handleListFilesTool(string(args), cfg.WorkDir), nil
}
