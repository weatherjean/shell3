package shell3

import (
	"errors"

	"github.com/weatherjean/shell3/internal/strutil"
)

// DispatchOpts tunes a host-initiated subagent job (see Session.Dispatch).
type DispatchOpts struct {
	// Description is the job title shown in task_list and the Jobs view. "" derives one from the prompt.
	Description string
	// WorkDir roots the child session's tools. "" inherits this session's
	// workdir; a relative path joins onto it (or onto the runtime root when
	// this session runs there).
	WorkDir string
	// Direct skips the notifier: the completion is delivered straight to the
	// main agent (for a cron dispatch: a fresh main-agent turn via the
	// CompletionHost) instead of being triaged.
	Direct bool
	// CronJob names the cron job this dispatch runs ("" for non-cron
	// dispatches). It routes the ⏰ post prefix and the ownerless wake path.
	CronJob string
	// Note is triage context handed to the notifier with the completion —
	// for cron, the job's prompt, so the judge knows what the job is FOR,
	// not just what it said.
	Note string
}

// Dispatch fires a fire-and-forget subagent job on the in-process job runtime —
// the same path the task tool uses. It is the host-side entry for scheduled
// (cron) prompts. The returned id is a normal job id (subN): the job shows up
// in Jobs()/task_list/the Jobs view, respects the background concurrency cap,
// and injects a capped result summary into this session's context on
// completion. Unlike the task tool, Dispatch does not enforce the agent's
// registered-subagent allowlist — the host decides what to run; agent must name a
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
		direct:  opts.Direct,
		cronJob: opts.CronJob,
		note:    opts.Note,
	})
}
