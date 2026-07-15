package shell3

import (
	"errors"

	"github.com/weatherjean/shell3/internal/strutil"
)

// DispatchOpts tunes a host-initiated subagent job (see Session.Dispatch).
type DispatchOpts struct {
	// Description is the job title shown in task_list and the dashboard's
	// background view. "" derives one from the prompt.
	Description string
	// WorkDir roots the child session's tools. "" inherits this session's
	// workdir; a relative path joins onto it (or onto the runtime root when
	// this session runs there).
	WorkDir string
	// Notify wakes the session when the job completes, so the host can RunQueued
	// a turn that narrates the result. False queues the completion notice
	// quietly for the agent's next turn — except on failure: a failed job
	// always wakes, so an unattended host still surfaces errors.
	Notify bool
}

// Dispatch fires a fire-and-forget subagent job on the in-process job runtime —
// the same path the task tool uses. It is the host-side entry for scheduled
// (cron) prompts. The returned id is a normal job id (subN): the job shows up
// in Jobs()/task_list/:background, respects the background concurrency cap,
// and injects a capped result summary into this session's context on
// completion. Unlike the task tool, Dispatch does not enforce the agent's
// tools.subagents allowlist — the host decides what to run; agent must name a
// declared subagent (or "" for the default agent).
func (s *Session) Dispatch(agent, prompt string, opts DispatchOpts) (string, error) {
	rt := s.runtimeHandle()
	if rt == nil || rt.jobs == nil {
		return "", errors.New("dispatch: session has no runtime")
	}
	desc := opts.Description
	if desc == "" {
		desc = strutil.Truncate(prompt, 60)
	}
	return rt.jobs.startSubagent(s, agent, prompt, desc, subagentOpts{
		workDir: opts.WorkDir,
		quiet:   !opts.Notify,
	})
}
