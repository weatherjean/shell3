package chat

import (
	"context"
	"encoding/json"
	"fmt"
)

// TaskHandler implements the background-only `task` tool: it launches a subagent
// (child session) and returns immediately.
type TaskHandler struct{}

func (TaskHandler) Name() string { return "task" }

func (TaskHandler) Execute(ctx context.Context, id string, args json.RawMessage, cfg ToolConfig) (string, error) {
	var p struct {
		SubagentType string `json:"subagent_type"`
		Prompt       string `json:"prompt"`
		Description  string `json:"description"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("task: invalid args: %w", err)
	}
	if p.Prompt == "" {
		return "", fmt.Errorf("task: prompt is required")
	}
	if cfg.StartSubagent == nil {
		return "error: subagents are not available", nil
	}
	subID, err := cfg.StartSubagent(p.SubagentType, p.Prompt, p.Description)
	if err != nil {
		return "error: " + err.Error(), nil // surfaced to the model (depth/cap/unknown agent)
	}
	return fmt.Sprintf("started subagent %s (@%s). You'll be notified when it finishes; keep working, don't poll.", subID, p.SubagentType), nil
}
