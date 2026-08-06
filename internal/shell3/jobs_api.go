package shell3

import (
	"errors"
	"time"
)

// JobProgress is an incremental progress event emitted on the JobEvents() bus
// for each background job. Chunk events carry a non-empty Chunk field; the
// terminal event has Done=true and an empty Chunk (plus Summary for subagents).
// Parent is the parent session's registry name (same value bgJob.parentID holds).
type JobProgress struct {
	JobID   string
	Parent  string
	Kind    JobKind
	Title   string
	Chunk   string // incremental rendered text; empty on the terminal event
	Done    bool
	Summary string // subagent jobs only; empty for command jobs
}

// JobInfo is the public projection of one background job (a bash_bg process or
// a fire-and-forget subagent) for a front-end to display. Done jobs are
// retained in-memory for the session lifetime (up to 100) so a front-end can show
// their final output and transcript.
type JobInfo struct {
	ID string
	// Cmd is the command text for command jobs and the model-supplied
	// description for subagent jobs (whose agent name is in Agent).
	Cmd string
	// Agent is the spawned agent's name for subagent jobs; "" for commands.
	Agent     string
	StartedAt time.Time
	Kind      JobKind
	ParentID  string
	Done      bool      // true once the job has finished
	Exit      *int      // command jobs: exit code (nil while running or for subagents)
	Summary   string    // subagent jobs: final assistant text (empty for command jobs)
	Error     string    // subagent jobs: last turn error ("" = clean run)
	EndedAt   time.Time // zero while running
	// ChildOpen is true for a subagent job whose child session is still open —
	// either still running its main turn, or lingering after Done=true to run
	// follow-up turns for a bash_bg that outlived the turn (see bgJob.lingering /
	// jobManager.runningJobIDs). A caller that wants to know whether a subagent
	// job can still produce more output (an agent_update notice, more job
	// events) must check this rather than Done alone: Done flips true at
	// main-turn end even while the child lingers. Always false for command jobs.
	ChildOpen bool
}

// JobEvents exposes the owning Runtime's background-job progress stream so a
// single-session front-end created via Start can live-tail jobs without holding
// a separate *Runtime handle. Returns nil when the session has no runtime.
func (s *Session) JobEvents() <-chan JobProgress {
	rt := s.runtimeHandle()
	if rt == nil {
		return nil
	}
	return rt.JobEvents()
}

// Jobs lists the live background jobs for this session's project — bash_bg
// processes and in-process subagents — newest first. Returns nil when the
// in-process job runtime is unavailable. (Backs /jobs.)
func (s *Session) Jobs() []JobInfo {
	rt := s.runtimeHandle() // snapshot under s.mu: doClose nils s.runtime concurrently
	if rt == nil || rt.jobs == nil {
		return nil
	}
	return rt.jobs.list()
}

// JobOutput returns the in-memory output buffer of a background command job,
// or "" when the job runtime is unavailable or the job is a subagent.
func (s *Session) JobOutput(id string) string {
	rt := s.runtimeHandle()
	if rt == nil || rt.jobs == nil {
		return ""
	}
	return rt.jobs.output(id)
}

// JobTranscript returns the stored transcript of a background SUBAGENT
// job's child session, or "" when the job runtime is unavailable or the job is
// a command (not a subagent). /job <id> renders this instead
// of the plain stdout log when present — see JobOutput for the fallback.
func (s *Session) JobTranscript(id string) string {
	rt := s.runtimeHandle()
	if rt == nil || rt.jobs == nil {
		return ""
	}
	return rt.jobs.transcript(id)
}

// KillJob cancels one background job (/cancel <id>). For
// command jobs this sends a cancellation signal; for subagent jobs it cancels
// the child session's context. It does not block; the job leaves the live list
// once it exits.
func (s *Session) KillJob(id string) error {
	rt := s.runtimeHandle()
	if rt == nil || rt.jobs == nil {
		return errors.New("shell3: no job runtime")
	}
	return rt.jobs.cancel(id)
}

// KillRunningJobs kills every live background job on the session — commands
// and subagents alike — and reports how many were killed. The shared half of
// the front-ends' /stop.
func (s *Session) KillRunningJobs() (killed int) {
	for _, j := range s.Jobs() {
		if !j.Done {
			if err := s.KillJob(j.ID); err == nil {
				killed++
			}
		}
	}
	return killed
}
