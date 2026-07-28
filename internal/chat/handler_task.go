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
		Direct       bool   `json:"direct"`
		Note         string `json:"note"`
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
	subID, err := cfg.StartSubagent(p.SubagentType, p.Prompt, p.Description, p.Direct, p.Note)
	if err != nil {
		return "error: " + err.Error(), nil // surfaced to the model (cap/unknown agent)
	}
	if p.Direct {
		return fmt.Sprintf("started subagent %s (@%s, direct). You'll be woken with its result; keep working, don't poll.", subID, p.SubagentType), nil
	}
	return fmt.Sprintf("started subagent %s (@%s). The notifier will triage its completion; keep working, don't poll.", subID, p.SubagentType), nil
}
