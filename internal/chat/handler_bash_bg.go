package chat

import (
	"context"
	"encoding/json"
	"fmt"
)

// BashBgHandler starts a background shell command on the managed in-process
// job runtime (via cfg.StartBashBg, wired to the internal/shell3 jobManager).
// The job runs as a goroutine-supervised child of the session; on completion
// the notifier triages the result (direct:true skips triage and delivers the
// notice straight back to this session) — there is no detached pid or log
// path to poll.
type BashBgHandler struct{}

func (BashBgHandler) Name() string { return "bash_bg" }

func (BashBgHandler) Execute(ctx context.Context, id string, args json.RawMessage, cfg ToolConfig) (string, error) {
	var p struct {
		Command string `json:"command"`
		Workdir string `json:"workdir"`
		Direct  bool   `json:"direct"`
		Note    string `json:"note"`
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
	jobID, err := cfg.StartBashBg(p.Command, wd, argv, nil, p.Direct, p.Note)
	if err != nil {
		return "", fmt.Errorf("bash_bg: %w", err)
	}
	// The mechanism is spelled out because "do not poll" alone does not hold:
	// a model that thinks ending the turn abandons the user will sit in a
	// task_status/sleep loop unless told the wake is how the answer arrives.
	if p.Direct {
		return fmt.Sprintf("started background job %s (direct)\n"+
			"Its completion notice will wake you: it extends this reply if the job finishes "+
			"before your turn ends, and arrives as a new message otherwise — so finish what "+
			"else you have, tell the user the job is running, and END YOUR TURN. "+
			"Do not poll task_status and do not sleep-and-recheck in bash; waiting in-turn "+
			"blocks the conversation without making the job faster.", jobID), nil
	}
	return fmt.Sprintf("started background job %s\n"+
		"The notifier will triage its completion (you may or may not hear back; failures always surface). "+
		"Do not poll — finish your turn; if the user is waiting on this result, "+
		"you should have set direct:true instead.", jobID), nil
}
