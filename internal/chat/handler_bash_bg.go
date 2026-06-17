package chat

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/weatherjean/shell3/internal/bgjobs"
)

// BashBgHandler spawns a detached background process for the bash_bg tool.
// Output is a short human-readable block the model can follow up on with
// the regular bash tool (cat .shell3_project/runs/jobs/*.status, tail <log>, kill <pid>).
type BashBgHandler struct{}

func (BashBgHandler) Name() string { return "bash_bg" }

func (BashBgHandler) Execute(ctx context.Context, id string, args json.RawMessage, cfg ToolConfig) (string, error) {
	var p struct {
		Command string `json:"command"`
		Workdir string `json:"workdir"`
	}
	if err := json.Unmarshal(args, &p); err != nil {
		return "", fmt.Errorf("bash_bg: invalid args: %w", err)
	}
	if p.Command == "" {
		return "", fmt.Errorf("bash_bg: command is required")
	}
	if cfg.RunsDir == "" {
		return "", fmt.Errorf("bash_bg: background jobs require a runs directory")
	}
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
	// Display the original command regardless of any runner swap. The job is
	// recorded under cfg.RunsDir/jobs/ as a status file.
	job, err := bgjobs.Start(cfg.RunsDir, argv, p.Command, wd, nil)
	if err != nil {
		return "", fmt.Errorf("bash_bg: %w", err)
	}
	out := fmt.Sprintf(
		"started %s\npid: %d\nlog: %s\n\nmanage with bash:\n  status: kill -0 %d  # exits 0 if alive, 1 if dead\n  output: tail -n 200 %s\n  kill:   kill %d         # or 'kill -- -%d' for whole group\n  list:   cat .shell3_project/runs/jobs/*.status\n",
		job.ID, job.PID, job.Log, job.PID, job.Log, job.PID, job.PID,
	)
	return out, nil
}
