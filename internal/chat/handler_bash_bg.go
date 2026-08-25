package chat

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/weatherjean/shell3/internal/notify"
)

// BashBgHandler starts a background shell command on the managed in-process
// job runtime (via cfg.StartBashBg, wired to the internal/shell3 jobManager).
// The job runs as a goroutine-supervised child of the session; its completion
// arrives as a task report (report:"raw" posts the job's own output to the user
// instead) — there is no detached pid or log path to poll.
type BashBgHandler struct{}

func (BashBgHandler) Name() string { return "bash_bg" }

func (BashBgHandler) Execute(ctx context.Context, id string, args json.RawMessage, cfg ToolConfig) (string, error) {
	var p struct {
		Command string `json:"command"`
		Workdir string `json:"workdir"`
		Report  string `json:"report"`
		Note    string `json:"note"`
		// Direct is the REMOVED arg, decoded only so it can be REFUSED.
		// json.Unmarshal ignores unknown fields, so without this a model
		// still writing direct:true — from a kit prompt, a skill, or its own
		// history in a long-lived conversation — would silently get auto
		// mode: exactly the "the user was waiting and nobody said so" failure
		// report: exists to remove.
		Direct *bool `json:"direct"`
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
	if p.Direct != nil {
		return directRemovedMsg, nil
	}
	mode, err := notify.ParseReportMode(p.Report)
	if err != nil {
		return "", fmt.Errorf("bash_bg: %w", err)
	}
	argv, blockMsg, blocked := gateBash(ctx, cfg, "bash_bg", p.Command, string(args))
	if blocked {
		return blockMsg, nil
	}
	wd := p.Workdir
	if wd == "" {
		wd = cfg.WorkDir
	}
	jobID, err := cfg.StartBashBg(p.Command, wd, argv, nil, mode, p.Note)
	if err != nil {
		return "", fmt.Errorf("bash_bg: %w", err)
	}
	return startedJobNotice("started background job "+jobID, mode), nil
}
