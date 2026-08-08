//go:build unix

package webui

import (
	"net/http"
	"strings"
	"time"

	"github.com/weatherjean/shell3/internal/cron"
	"github.com/weatherjean/shell3/internal/shell3"
)

// The operational views: what the agent is doing in the background, what is
// scheduled, and what it has done before. Everything here is read-only except
// cancelling a job and firing a cron job by hand.

// ---------------------------------------------------------------------- jobs

type jobDetail struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"` // "bash_bg" | "subagent"
	Label    string `json:"label"`
	Status   string `json:"status"` // running | done | failed
	Started  string `json:"startedAt"`
	Ended    string `json:"endedAt,omitempty"`
	Elapsed  int    `json:"elapsedSeconds"`
	Exit     *int   `json:"exit,omitempty"`
	Summary  string `json:"summary,omitempty"`
	Error    string `json:"error,omitempty"`
	Parent   string `json:"parent,omitempty"`
	HasChild bool   `json:"childOpen"`
}

func describeJob(job shell3.JobInfo) jobDetail {
	label, kind := job.Cmd, "bash_bg"
	if job.Kind == shell3.JobSubagent {
		label, kind = job.Agent, "subagent"
	}
	status := "running"
	switch {
	case job.Error != "":
		status = "failed"
	case job.Done:
		status = "done"
	}

	// A finished job's elapsed time is how long it took, not how long ago it
	// started — the difference matters once something has been done for hours.
	end := time.Now()
	if job.Done && !job.EndedAt.IsZero() {
		end = job.EndedAt
	}

	detail := jobDetail{
		ID: job.ID, Kind: kind, Label: label, Status: status,
		Started: job.StartedAt.Format(time.RFC3339),
		Elapsed: int(end.Sub(job.StartedAt).Seconds()),
		Exit:    job.Exit, Summary: job.Summary, Error: job.Error,
		Parent: job.ParentID, HasChild: job.ChildOpen,
	}
	if !job.EndedAt.IsZero() {
		detail.Ended = job.EndedAt.Format(time.RFC3339)
	}
	return detail
}

// handleJobDetail returns one job plus its output: a command's captured stdout,
// or a subagent's stored transcript.
func (s *Server) handleJobDetail(w http.ResponseWriter, id string) {
	sess := s.anySession()
	if sess == nil {
		http.Error(w, "no such job", http.StatusNotFound)
		return
	}

	for _, job := range sess.Jobs() {
		if job.ID != id {
			continue
		}
		output := sess.JobTranscript(id)
		kind := "transcript"
		if output == "" {
			output, kind = sess.JobOutput(id), "output"
		}
		writeJSON(w, map[string]any{
			"job": describeJob(job), "output": output, "outputKind": kind,
		})
		return
	}
	http.Error(w, "no such job", http.StatusNotFound)
}

// ---------------------------------------------------------------------- cron

type cronJobResp struct {
	Name     string `json:"name"`
	Schedule string `json:"schedule"`
	Agent    string `json:"agent"`
	Prompt   string `json:"prompt,omitempty"`
	WorkDir  string `json:"workdir,omitempty"`
	Direct   bool   `json:"direct"`
	LastRun  string `json:"lastRun,omitempty"`
	LastJob  string `json:"lastJobId,omitempty"`
}

func (s *Server) handleCron(w http.ResponseWriter, _ *http.Request) {
	out := []cronJobResp{}

	// The live scheduler knows what has actually run; without one (no jobs
	// declared) fall back to the declared list so the view still explains
	// itself.
	source, _ := s.cronFuncs()
	if source != nil {
		for _, job := range source() {
			out = append(out, cronJobResp{
				Name: job.Name, Schedule: job.Schedule, Agent: job.Agent,
				Prompt: job.Prompt, WorkDir: job.WorkDir, Direct: job.Direct,
				LastRun: job.LastRun, LastJob: job.LastSubID,
			})
		}
	} else {
		for _, job := range s.rt.Cron() {
			out = append(out, cronJobResp{
				Name: job.Name, Schedule: job.Schedule,
				Agent: job.Agent, Prompt: job.Prompt, Direct: job.Direct,
			})
		}
	}
	writeJSON(w, map[string]any{"jobs": out, "armed": source != nil})
}

// handleCronRun fires a scheduled job now. The result travels the usual route —
// the notifier decides whether it reaches the bell or wakes the agent.
func (s *Server) handleCronRun(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	_, run := s.cronFuncs()
	if run == nil {
		http.Error(w, "no cron scheduler is armed", http.StatusConflict)
		return
	}
	if err := run(name); err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]bool{"started": true})
}

// SetCronSource wires the live scheduler in: its job list for the Cron view,
// and its manual fire for the run button. nil disarms both — a reload that
// removed the last cron/ file leaves nothing to list or fire.
func (s *Server) SetCronSource(sched *cron.Scheduler) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sched == nil {
		s.cronSource, s.runCron = nil, nil
		return
	}
	s.cronSource = sched.Jobs
	s.runCron = sched.Run
}

// cronFuncs reads the scheduler wiring under the lock — it is swapped on
// every reload, racing the HTTP handlers that call it.
func (s *Server) cronFuncs() (func() []cron.JobStatus, func(name string) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cronSource, s.runCron
}

// ---------------------------------------------------------------------- runs

type runResp struct {
	ID       string `json:"id"`
	Started  string `json:"startedAt"`
	Ended    string `json:"endedAt,omitempty"`
	LastAt   string `json:"lastAt"`
	Messages int    `json:"messages"`
	Preview  string `json:"preview"`
	/** Set when this run is the session behind a browser thread. */
	ThreadID string `json:"threadId,omitempty"`
}

// handleRuns lists stored sessions, newest first — including the subagent and
// cron child sessions that never appear in the conversation list.
// runsLimit caps the listing. A store with months of history would otherwise
// send megabytes to render a page nobody scrolls to the end of.
const runsLimit = 100

func (s *Server) handleRuns(w http.ResponseWriter, _ *http.Request) {
	// One extra tells us whether there are more without counting them all.
	sessions, err := s.rt.PastSessions(runsLimit + 1)
	if err != nil {
		http.Error(w, "could not read the run store", http.StatusInternalServerError)
		return
	}

	// Label the runs that are conversations, so the rest read as background work.
	threadOf := map[string]string{}
	for _, rec := range s.threads.list() {
		if rec.SessionID != "" {
			threadOf[rec.SessionID] = rec.ID
		}
	}

	// Say when the list is cut short rather than presenting it as everything.
	truncated := len(sessions) > runsLimit
	if truncated {
		sessions = sessions[:runsLimit]
	}

	out := make([]runResp, 0, len(sessions))
	for _, meta := range sessions {
		run := runResp{
			ID: meta.ID, Started: meta.StartedAt.Format(time.RFC3339),
			LastAt:   meta.LastAt.Format(time.RFC3339),
			Messages: meta.NumMsgs, Preview: meta.Preview,
			ThreadID: threadOf[meta.ID],
		}
		if !meta.EndedAt.IsZero() {
			run.Ended = meta.EndedAt.Format(time.RFC3339)
		}
		out = append(out, run)
	}
	writeJSON(w, map[string]any{"runs": out, "truncated": truncated, "limit": runsLimit})
}

type transcriptEntry struct {
	Role      string     `json:"role"`
	Content   string     `json:"content,omitempty"`
	Reasoning string     `json:"reasoning,omitempty"`
	ToolName  string     `json:"toolName,omitempty"`
	ToolCalls []toolCall `json:"toolCalls,omitempty"`
}

type toolCall struct {
	Name string `json:"name"`
	Args string `json:"args,omitempty"`
}

// handleRunTranscript returns one session's full transcript, tool calls and
// reasoning included — the record of how the work was actually done, which the
// chat view deliberately does not replay.
func (s *Server) handleRunTranscript(w http.ResponseWriter, id string) {
	// An unknown id otherwise reads back as an empty run, which the browser
	// cannot tell from a real session that happens to have no messages.
	if !s.runExists(id) {
		http.Error(w, "no such run", http.StatusNotFound)
		return
	}
	history, err := s.rt.SessionMessages(id)
	if err != nil {
		http.Error(w, "could not read this run", http.StatusInternalServerError)
		return
	}

	out := make([]transcriptEntry, 0, len(history))
	for _, entry := range history {
		item := transcriptEntry{
			Role: entry.Role, Content: entry.Content,
			Reasoning: entry.Reasoning, ToolName: entry.ToolName,
		}
		for _, call := range entry.ToolCalls {
			item.ToolCalls = append(item.ToolCalls, toolCall{Name: call.Name, Args: call.Args})
		}
		out = append(out, item)
	}
	writeJSON(w, map[string]any{"id": id, "entries": out})
}

// runExists reports whether a run id names a stored session. Asked of the
// store, not the disk: a runs/<id>/ directory exists only once a job logs
// into it, so a disk check would 404 nearly every run the list serves.
func (s *Server) runExists(id string) bool {
	return id != "" && s.rt.SessionExists(id)
}

// routeRuns dispatches /api/runs/{id} and /api/runs/{id}/... .
func (s *Server) routeRuns(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/runs/")
	if id == "" {
		http.Error(w, "missing run id", http.StatusBadRequest)
		return
	}
	s.handleRunTranscript(w, id)
}

// routeCron dispatches /api/cron/{name}/run.
func (s *Server) routeCron(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/cron/")
	name, tail, _ := strings.Cut(rest, "/")
	if name == "" || tail != "run" {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	s.handleCronRun(w, r, name)
}
