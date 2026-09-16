package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// BashBgHandler starts a background shell command on the managed in-process
// job runtime (via cfg.StartBashBg, wired to the internal/shell3 jobManager).
// The job runs as a goroutine-supervised child of the session. Its completion
// is persisted to the filesystem inbox; there is no detached pid to poll.
type BashBgHandler struct{}

func startedJobNotice(head string) string {
	return head + ".\nIts completion will be saved to the durable inbox. A scheduled poll_in check runs in a later turn, leaving the conversation available while the command works."
}

// ParsePollIn bounds autonomous check-ins while allowing normal Go durations.
func ParsePollIn(value string) (time.Duration, error) {
	d, err := time.ParseDuration(value)
	if err != nil || d < time.Minute || d > 24*time.Hour {
		return 0, fmt.Errorf("poll_in must be a duration between 1m and 24h (for example 2m, 3m, or 5m)")
	}
	return d, nil
}

func (BashBgHandler) Name() string { return "bash_bg" }

func (BashBgHandler) Execute(ctx context.Context, id string, args json.RawMessage, cfg ToolConfig) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var p struct {
		Command string `json:"command"`
		Workdir string `json:"workdir"`
		PollIn  string `json:"poll_in"`
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(args, &fields); err != nil {
		return "", fmt.Errorf("bash_bg: invalid args: %w", err)
	}
	for field := range fields {
		if field != "command" && field != "workdir" && field != "poll_in" {
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
	var pollIn time.Duration
	if _, present := fields["poll_in"]; present {
		var err error
		pollIn, err = ParsePollIn(p.PollIn)
		if err != nil {
			return "", err
		}
		if cfg.StartBashBgPolled == nil {
			return "", fmt.Errorf("bash_bg: poll_in requires an interactive console or Telegram host; command was not started")
		}
	}
	argv := []string{"bash", "-c", p.Command}
	wd := p.Workdir
	if wd == "" {
		wd = cfg.WorkDir
	}
	var jobID string
	var err error
	if pollIn > 0 {
		jobID, err = cfg.StartBashBgPolled(p.Command, wd, argv, []string{"SHELL3_TOOL_CONTEXT=background"}, pollIn)
	} else {
		jobID, err = cfg.StartBashBg(p.Command, wd, argv, []string{"SHELL3_TOOL_CONTEXT=background"})
	}
	if err != nil {
		return "", fmt.Errorf("bash_bg: %w", err)
	}
	head := "started background job " + jobID
	if pollIn > 0 {
		head += "; one-shot follow-up scheduled in " + pollIn.String() + ", even if the command finishes first"
	}
	return startedJobNotice(head), nil
}
