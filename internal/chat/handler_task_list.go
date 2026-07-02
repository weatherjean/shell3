package chat

import (
	"context"
	"encoding/json"
)

// TaskListHandler implements the task_list tool: lists all background tasks
// (running and done) for the active runtime.
type TaskListHandler struct{}

func (TaskListHandler) Name() string { return "task_list" }

func (TaskListHandler) Execute(_ context.Context, _ string, _ json.RawMessage, cfg ToolConfig) (string, error) {
	if cfg.ListJobs == nil {
		return "no background tasks", nil
	}
	return cfg.ListJobs(), nil
}
