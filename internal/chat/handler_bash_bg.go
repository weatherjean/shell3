package chat

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/weatherjean/shell3/internal/bgjobs"
)

// BashBgHandler spawns a detached background process for the bash_bg tool.
// Output is a short human-readable block the model can follow up on with
// the regular bash tool (cat .shell3/bg.json, tail <log>, kill <pid>).
type BashBgHandler struct{}

func (BashBgHandler) Name() string { return "bash_bg" }

func (BashBgHandler) Execute(ctx context.Context, id string, args json.RawMessage, cfg ToolConfig) (string, error) {
	// NotifyOnExit defaults to true (a missing key means "notify"): a pointer
	// distinguishes an explicit false from an omitted key. A subagent spawn sets
	// it false so the child's own agent_done is the only notification (no
	// duplicate bg_done); plain bg jobs leave it unset and keep bg_done.
	var p struct {
		Command      string `json:"command"`
		Workdir      string `json:"workdir"`
		NotifyOnExit *bool  `json:"notify_on_exit"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("bash_bg: invalid args: %w", err)
	}
	if p.Command == "" {
		return "", fmt.Errorf("bash_bg: command is required")
	}
	notifyOnExit := p.NotifyOnExit == nil || *p.NotifyOnExit
	wd := p.Workdir
	if wd == "" {
		wd = cfg.WorkDir
	}
	// shell3.wrap_bash applies to bash_bg too: rewrite, swap the runner, or
	// block before the command is backgrounded. Nil hook = no wrapping.
	argv := []string{"bash", "-c", p.Command}
	if cfg.WrapBash != nil {
		a, allowed, reason, err := cfg.WrapBash(ctx, p.Command)
		if err != nil {
			return "error: wrap_bash failed: " + err.Error(), nil
		}
		if !allowed {
			return "error: blocked by wrap_bash: " + reason, nil
		}
		argv = a
	}
	// cfg.SinkPath is the session's notification sink (empty for front-ends
	// that don't wire one): the reaper appends a bg_done notification there on
	// exit so the host can tell the agent the background job finished.
	// Display the original command in bg.json/sink regardless of any runner swap.
	job, err := bgjobs.Start(argv, p.Command, wd, nil, cfg.SinkPath, notifyOnExit)
	if err != nil {
		return "", fmt.Errorf("bash_bg: %w", err)
	}
	out := fmt.Sprintf(
		"started %s\npid: %d\nlog: %s\n\nmanage with bash:\n  status: kill -0 %d  # exits 0 if alive, 1 if dead\n  output: tail -n 200 %s\n  kill:   kill %d         # or 'kill -- -%d' for whole group\n  list:   cat %s/.shell3/bg.json\n",
		job.ID, job.PID, job.Log, job.PID, job.Log, job.PID, job.PID, wd,
	)
	return out, nil
}
