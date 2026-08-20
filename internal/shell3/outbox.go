package shell3

import (
	"encoding/json"
	"os"
	"syscall"
	"time"
)

// The completion outbox makes background-work delivery restart-durable
// (runs.Store's outbox table, schema v7). Two row kinds:
//
//   - "event": a finished job's CompletionEvent, written by dispatchCompletion
//     before routing and deleted after the front-end hand-off returns.
//     Delivery is at-least-once: a crash between hand-off and delete
//     duplicates the report at the next boot — never loses it. Do not "fix"
//     the ordering to delete first.
//   - "running": a started job's marker, written at start and deleted when the
//     job finishes. A marker still present at boot (from a dead process) is a
//     job whose result was lost in flight; recovery reports it as such.
//
// RecoverCompletions is the boot-time pass, run by the long-lived front-ends
// (telegram, serve) after their CompletionHost is installed — never by ask,
// which matches the janitors' start-time-only shape. Rows written by a
// CONCURRENT process (an ask running alongside the bot) are protected by the
// marker's PID: a live PID is skipped, and ask deletes its own rows as its
// jobs finish. An ask killed mid-job does leave rows a later bot start will
// report — deliberate: a completion is never silently lost, whoever spawned
// it.

// runningMarker is the JSON body of a "running" outbox row.
type runningMarker struct {
	PID       int       `json:"pid"`
	Kind      string    `json:"kind"` // "command" | "subagent"
	JobID     string    `json:"job_id"`
	Title     string    `json:"title"`
	Agent     string    `json:"agent,omitempty"`
	CronJob   string    `json:"cron_job,omitempty"`
	OwnerID   string    `json:"owner_id,omitempty"`
	LogPath   string    `json:"log_path,omitempty"`
	ChildID   string    `json:"child_id,omitempty"`
	StartedAt time.Time `json:"started_at"`
}

// persistEvent writes ev's outbox row, returning 0 when no store is wired or
// the write fails — durability is best-effort on top of normal delivery, and
// a store hiccup must not block the completion itself.
func (m *jobManager) persistEvent(ev CompletionEvent) int64 {
	if m.rt == nil || m.rt.store == nil {
		return 0
	}
	b, err := json.Marshal(ev)
	if err != nil {
		return 0
	}
	id, err := m.rt.store.OutboxPut("event", string(b))
	if err != nil {
		return 0
	}
	return id
}

// deleteOutboxRow removes one outbox row; 0 (nothing persisted) is a no-op.
func (m *jobManager) deleteOutboxRow(id int64) {
	if id == 0 || m.rt == nil || m.rt.store == nil {
		return
	}
	_ = m.rt.store.OutboxDelete(id)
}

// shutdownCancelled reports whether jobID was cancelled by cancelAll (as
// opposed to finishing on its own while the runtime closes).
func (m *jobManager) shutdownCancelled(jobID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	j := m.jobs[jobID]
	return j != nil && j.shutdownCancel
}

// putRunningMarker persists j's "running" row and records the row id on the
// job. Call without holding m.mu (SQLite write); the id store takes the lock.
func (m *jobManager) putRunningMarker(j *bgJob, kind, ownerID string) {
	if m.rt == nil || m.rt.store == nil {
		return
	}
	mk := runningMarker{
		PID: os.Getpid(), Kind: kind, JobID: j.id, Title: j.title,
		Agent: j.agent, CronJob: j.cronJob, OwnerID: ownerID,
		LogPath: j.logPath, ChildID: j.childID, StartedAt: j.startedAt,
	}
	b, err := json.Marshal(mk)
	if err != nil {
		return
	}
	id, err := m.rt.store.OutboxPut("running", string(b))
	if err != nil {
		return
	}
	m.mu.Lock()
	j.markerID = id
	m.mu.Unlock()
}

// clearRunningMarker deletes j's "running" row when the job finishes — unless
// shutdown cancelled it, in which case the marker is the boot-time record
// that the job's result was lost.
func (m *jobManager) clearRunningMarker(j *bgJob) {
	m.mu.Lock()
	id := j.markerID
	keep := j.shutdownCancel
	j.markerID = 0
	m.mu.Unlock()
	if keep {
		return
	}
	m.deleteOutboxRow(id)
}

// rememberUndelivered records an event row whose user-facing post failed to
// send, for RedeliverUndelivered to retry. rowID 0 (nothing persisted — no
// store) has nothing to retry from and is skipped.
func (m *jobManager) rememberUndelivered(rowID int64) {
	if rowID == 0 {
		return
	}
	m.mu.Lock()
	if m.undelivered == nil {
		m.undelivered = make(map[int64]struct{})
	}
	m.undelivered[rowID] = struct{}{}
	m.mu.Unlock()
}

// takeUndelivered snapshots and clears the undelivered set. A row whose retry
// fails again re-enters the set via dispatchCompletion's own bookkeeping.
func (m *jobManager) takeUndelivered() map[int64]struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := m.undelivered
	m.undelivered = nil
	return ids
}

// RedeliverUndelivered retries completion events whose user-facing post
// failed to send while this process was running (a Telegram outage riding out
// a night, say). Same ordering contract as RecoverCompletions: re-dispatch
// first — which persists a fresh row and, on another send failure, re-enters
// the retry set — then delete the stale row. Call it periodically from the
// front-end host (wireHost's ticker); it is a no-op when nothing failed.
func (rt *Runtime) RedeliverUndelivered() int {
	if rt.store == nil {
		return 0
	}
	ids := rt.jobs.takeUndelivered()
	if len(ids) == 0 {
		return 0
	}
	rows, err := rt.store.OutboxLoadAll()
	if err != nil {
		// Store hiccup: put the ids back so a later tick retries.
		for id := range ids {
			rt.jobs.rememberUndelivered(id)
		}
		return 0
	}
	redelivered := 0
	for _, r := range rows {
		if _, ok := ids[r.ID]; !ok || r.Kind != "event" {
			continue
		}
		var ev CompletionEvent
		if err := json.Unmarshal([]byte(r.JSON), &ev); err != nil {
			_ = rt.store.OutboxDelete(r.ID)
			continue
		}
		rt.jobs.dispatchCompletion(ev)
		_ = rt.store.OutboxDelete(r.ID)
		redelivered++
	}
	return redelivered
}

// pidAlive reports whether pid names a live process (signal 0 probe). An
// unsupported platform errs toward "dead", which at worst redelivers a row
// its owner was about to delete.
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

// RecoverCompletions redelivers what a previous process left in the outbox:
// undelivered completion events, and running markers of jobs a dead process
// never finished (reported as a failure so the loss is visible). Call once at
// startup, after SetCompletionHost. Returns the number of rows recovered.
func (rt *Runtime) RecoverCompletions() int {
	if rt.store == nil {
		return 0
	}
	rows, err := rt.store.OutboxLoadAll()
	if err != nil {
		return 0
	}
	recovered := 0
	for _, r := range rows {
		switch r.Kind {
		case "event":
			var ev CompletionEvent
			if err := json.Unmarshal([]byte(r.JSON), &ev); err != nil {
				_ = rt.store.OutboxDelete(r.ID)
				continue
			}
			// Redeliver FIRST, delete the stale row after: dispatchCompletion
			// persists its own fresh row, so a crash anywhere in between leaves
			// at least one row for the next boot — a duplicate report at worst,
			// never a lost one. Deleting first would open a loss window.
			ev.Note = joinNote(ev.Note, "recovered after a shell3 restart — this completion was never delivered")
			rt.jobs.dispatchCompletion(ev)
			_ = rt.store.OutboxDelete(r.ID)
			recovered++
		case "running":
			var mk runningMarker
			if err := json.Unmarshal([]byte(r.JSON), &mk); err != nil {
				_ = rt.store.OutboxDelete(r.ID)
				continue
			}
			if pidAlive(mk.PID) {
				continue // a concurrent process (ask) still owns this job
			}
			ev := CompletionEvent{
				Kind: EvBashBg, JobID: mk.JobID, Title: mk.Title,
				Agent: mk.Agent, CronJob: mk.CronJob,
				ErrText: "was still running when shell3 stopped; its result was lost",
				Detail:  mk.LogPath, RunID: mk.ChildID, OwnerID: mk.OwnerID,
			}
			if mk.Kind == "subagent" {
				ev.Kind = EvSubagent
				if mk.CronJob != "" {
					ev.Kind = EvCron
				}
			}
			// Same ordering as the event branch: report first, delete after,
			// so a crash mid-recovery re-reports rather than losing the loss.
			rt.jobs.dispatchCompletion(ev)
			_ = rt.store.OutboxDelete(r.ID)
			recovered++
		}
	}
	return recovered
}
