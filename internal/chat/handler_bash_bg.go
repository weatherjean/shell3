package chat

import (
	"context"
	"encoding/json"
	"fmt"
)

// BashBgHandler starts a background shell command on the managed in-process
// job runtime (via cfg.StartBashBg, wired to the internal/shell3 jobManager). The
// job runs as a goroutine-supervised child of the session; the agent is woken
// with a completion notice on a later turn — there is no detached pid or log
// path to poll.
type BashBgHandler struct{}

func (BashBgHandler) Name() string { return "bash_bg" }

func (BashBgHandler) Execute(ctx context.Context, id string, args json.RawMessage, cfg ToolConfig) (string, error) {
	var p struct {
		Command   string `json:"command"`
		Workdir   string `json:"workdir"`
		ForceWake bool   `json:"force_wake"`
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
	jobID, err := cfg.StartBashBg(p.Command, wd, argv, nil, p.ForceWake)
	if err != nil {
		return "", fmt.Errorf("bash_bg: %w", err)
	}
	return fmt.Sprintf("started background job %s\nYou'll get a completion notice on your next turn. Do not poll.", jobID), nil
}
