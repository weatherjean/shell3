package shell3

import "time"

// JobProgress is an incremental progress event emitted on the JobEvents() bus
// for each background job. Chunk events carry a non-empty Chunk field; the
// terminal event has Done=true and an empty Chunk.
// Parent is the parent session's registry name (same value bgJob.parentID holds).
type JobProgress struct {
	JobID  string
	Parent string
	Kind   JobKind
	Title  string
	Chunk  string // incremental rendered text; empty on the terminal event
	Done   bool
}

// JobInfo is the public projection of one bash_bg process. Done jobs are
// retained in-memory for the session lifetime (up to 100) so a front-end can show
// their final output and transcript.
type JobInfo struct {
	ID        string
	Cmd       string
	StartedAt time.Time
	Kind      JobKind
	ParentID  string
	// ParentSession is the parent's runs session id — the directory this job's
	// tee'd log lives under (runs/<ParentSession>/jobs/<ID>.log). ParentID is
	// the in-process handle and does NOT name a directory.
	ParentSession string
	Done          bool      // true once the job has finished
	Exit          *int      // nil while running
	EndedAt       time.Time // zero while running
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

// Jobs lists the runtime's bash_bg processes, newest first. Returns nil when the
// in-process job runtime is unavailable. (Backs the Jobs view.)
func (s *Session) Jobs() []JobInfo {
	rt := s.runtimeHandle() // snapshot under s.mu: doClose nils s.runtime concurrently
	if rt == nil || rt.jobs == nil {
		return nil
	}
	return rt.jobs.list()
}

// KilledJob describes one job /superstop killed — enough for the summary the
// user and the agent both read.
type KilledJob struct {
	ID      string
	Title   string
	Kind    string // "command"
	Runtime time.Duration
}

// KillAllForStop kills every live background command with completion routing
// suppressed: no failure posts or owner mail. The returned list supplies
// the one superstop summary that replaces
// them. The front-ends' /superstop primitive.
func (s *Session) KillAllForStop() []KilledJob {
	rt := s.runtimeHandle()
	if rt == nil || rt.jobs == nil {
		return nil
	}
	return rt.jobs.killAllForStop()
}
