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
	PID       int
	StartedAt time.Time
	Kind      JobKind
	Depth     int
	ParentID  string
	Done      bool      // true once the job has finished
	Exit      *int      // command jobs: exit code (nil while running or for subagents)
	Summary   string    // subagent jobs: final assistant text (empty for command jobs)
	Error     string    // subagent jobs: last turn error ("" = clean run)
	EndedAt   time.Time // zero while running
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
// in-process job runtime is unavailable. (Backs the dashboard's background view.)
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

// JobTranscript returns the messages.jsonl contents of a background SUBAGENT
// job's child session, or "" when the job runtime is unavailable or the job is
// a command (not a subagent). The dashboard's background view renders this instead
// of the plain stdout log when present — see JobOutput for the fallback.
func (s *Session) JobTranscript(id string) string {
	rt := s.runtimeHandle()
	if rt == nil || rt.jobs == nil {
		return ""
	}
	return rt.jobs.transcript(id)
}

// KillJob cancels one background job (the dashboard's cancel action). For
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
