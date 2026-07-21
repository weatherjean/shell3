package chat

import (
	"context"
	"encoding/json"
	"fmt"
)

// BashBgHandler starts a background shell command on the managed in-process
// job runtime (via cfg.StartBashBg, wired to the internal/shell3 jobManager). The
// job runs as a goroutine-supervised child of the session; completion wakes an
// idle agent with a notice (quiet:true queues clean exits for the next turn
// instead) — there is no detached pid or log path to poll.
type BashBgHandler struct{}

func (BashBgHandler) Name() string { return "bash_bg" }

func (BashBgHandler) Execute(ctx context.Context, id string, args json.RawMessage, cfg ToolConfig) (string, error) {
	var p struct {
		Command string `json:"command"`
		Workdir string `json:"workdir"`
		Quiet   bool   `json:"quiet"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("bash_bg: invalid args: %w", err)
	}
	if p.Command == "" {
		return "", fmt.Errorf("bash_bg: command is required")
	}
	if cfg.StartBashBg == nil {
		return "", fmt.Errorf("bash_bg: background jobs are not available")
	}
	argv, blockMsg, blocked := gateBash(ctx, cfg, "bash_bg", p.Command, string(args))
	if blocked {
		return blockMsg, nil
	}
	wd := p.Workdir
	if wd == "" {
		wd = cfg.WorkDir
	}
	jobID, err := cfg.StartBashBg(p.Command, wd, argv, nil, p.Quiet)
	if err != nil {
		return "", fmt.Errorf("bash_bg: %w", err)
	}
	if p.Quiet {
		return fmt.Sprintf("started background job %s (quiet)\nA clean exit queues its notice for your next turn; a failure wakes you. Do not poll.", jobID), nil
	}
	return fmt.Sprintf("started background job %s\nYou'll be woken with a completion notice when it finishes. Do not poll.", jobID), nil
}
