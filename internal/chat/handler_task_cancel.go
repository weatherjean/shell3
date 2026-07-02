package chat

import (
	"context"
	"encoding/json"
	"fmt"
)

// TaskCancelHandler implements the task_cancel tool: cancels a running
// background task and returns a short confirmation or error.
type TaskCancelHandler struct{}

func (TaskCancelHandler) Name() string { return "task_cancel" }

func (TaskCancelHandler) Execute(_ context.Context, _ string, args json.RawMessage, cfg ToolConfig) (string, error) {
	var p struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("task_cancel: invalid args: %w", err)
	}
	if p.ID == "" {
		return "error: id is required", nil
	}
	if cfg.CancelJob == nil {
		return fmt.Sprintf("no such task %s", p.ID), nil
	}
	return cfg.CancelJob(p.ID), nil
}
