package chat

import (
	"context"
	"encoding/json"
	"fmt"
)

// BashBgHandler starts a background shell command on the managed in-process
// job runtime (via cfg.StartBashBg, wired to the internal/shell3 jobManager).
// The job runs as a goroutine-supervised child of the session. Its completion
// is persisted to the filesystem inbox; there is no detached pid to poll.
type BashBgHandler struct{}

func startedJobNotice(head string) string {
	const dontPoll = "Do not poll or sleep-and-recheck in bash; waiting in-turn blocks the conversation without making the job faster."
	return head + ".\nIts completion will be saved to the durable inbox. Finish your turn. " + dontPoll
}

func (BashBgHandler) Name() string { return "bash_bg" }

func (BashBgHandler) Execute(ctx context.Context, id string, args json.RawMessage, cfg ToolConfig) (string, error) {
	var p struct {
		Command string `json:"command"`
		Workdir string `json:"workdir"`
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(args, &fields); err != nil {
		return "", fmt.Errorf("bash_bg: invalid args: %w", err)
	}
	for field := range fields {
		if field != "command" && field != "workdir" {
			return "", fmt.Errorf("bash_bg: unknown field %q", field)
		}
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
	argv := []string{"bash", "-c", p.Command}
	wd := p.Workdir
	if wd == "" {
		wd = cfg.WorkDir
	}
	jobID, err := cfg.StartBashBg(p.Command, wd, argv, nil)
	if err != nil {
		return "", fmt.Errorf("bash_bg: %w", err)
	}
	return startedJobNotice("started background job " + jobID), nil
}
