package chat

import (
	"context"
	"encoding/json"
	"fmt"
)

// TaskStatusHandler implements the task_status tool: returns one task's status
// and a truncated result (subagent transcript tail or command output tail).
type TaskStatusHandler struct{}

func (TaskStatusHandler) Name() string { return "task_status" }

func (TaskStatusHandler) Execute(_ context.Context, _ string, args json.RawMessage, cfg ToolConfig) (string, error) {
	var p struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("task_status: invalid args: %w", err)
	}
	if p.ID == "" {
		return "error: id is required", nil
	}
	if cfg.JobStatus == nil {
		return "error: task management is not available", nil
	}
	return cfg.JobStatus(p.ID), nil
}
