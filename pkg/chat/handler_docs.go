package chat

import (
	"context"
	"encoding/json"
)

// DocsHandler implements the shell3_docs built-in tool.
// The docs string is set at construction time from cfg.Docs.
type DocsHandler struct {
	docs string
}

func (DocsHandler) Name() string { return "shell3_docs" }

func (h DocsHandler) Execute(ctx context.Context, id string, args json.RawMessage, cfg ToolConfig) (string, error) {
	if h.docs == "" {
		return "Documentation not available.", nil
	}
	return h.docs, nil
}
