package chat

import (
	"context"
	"encoding/json"
	"fmt"
)

// taskByIDHandler is the shared shape of the task tools that take one {id}
// argument and call a ToolConfig-provided func: task_status and task_cancel.
type taskByIDHandler struct {
	name string
	// fn picks the ToolConfig callback (nil ⇒ task management unavailable).
	fn func(ToolConfig) func(string) string
}

// TaskStatusHandler implements the task_status tool: returns one task's status
// and a truncated result (subagent transcript tail or command output tail).
func TaskStatusHandler() ToolHandler {
	return taskByIDHandler{name: "task_status", fn: func(cfg ToolConfig) func(string) string { return cfg.JobStatus }}
}

// TaskCancelHandler implements the task_cancel tool: cancels a running
// background task and returns a short confirmation or error.
func TaskCancelHandler() ToolHandler {
	return taskByIDHandler{name: "task_cancel", fn: func(cfg ToolConfig) func(string) string { return cfg.CancelJob }}
}

func (h taskByIDHandler) Name() string { return h.name }

func (h taskByIDHandler) Execute(_ context.Context, _ string, args json.RawMessage, cfg ToolConfig) (string, error) {
	var p struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("%s: invalid args: %w", h.name, err)
	}
	if p.ID == "" {
		return "error: id is required", nil
	}
	if fn := h.fn(cfg); fn != nil {
		return fn(p.ID), nil
	}
	return "error: task management is not available", nil
}
