package shell3

import (
	"encoding/json"
	"os"
	"syscall"
	"time"

	"github.com/weatherjean/shell3/internal/runs"
)

// The completion outbox makes background-work delivery restart-durable
// (runs.Store's outbox table). Two row kinds:
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
// after their CompletionHost is installed, never by ask. A concurrent
// process's rows are protected by the marker's PID: a live PID is skipped, and
// ask deletes its own rows as jobs finish. An ask killed mid-job does leave
// rows a later bot start reports — deliberate, since a completion is never
// silently lost whoever spawned it.

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

// The store pointer is generation state guarded by Runtime.mu. Keep the lock
// through each short outbox operation so Reload cannot close the selected
// handle between the pointer read and the SQL call.
func (rt *Runtime) outboxPut(kind, body string) (int64, error) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.store == nil {
		return 0, nil
	}
	return rt.store.OutboxPut(kind, body)
}

func (rt *Runtime) outboxDelete(id int64) error {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.store == nil {
		return nil
	}
	return rt.store.OutboxDelete(id)
}

func (rt *Runtime) outboxLoadAll() ([]runs.OutboxRow, error) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.store == nil {
		return nil, nil
	}
	return rt.store.OutboxLoadAll()
}

// persistEvent writes ev's row, 0 when there is no store or the write fails:
// durability rides on top of normal delivery and must not block it.
func (m *jobManager) persistEvent(ev CompletionEvent) int64 {
	if m.rt == nil {
		return 0
	}
	b, err := json.Marshal(ev)
	if err != nil {
		return 0
	}
	id, err := m.rt.outboxPut("event", string(b))
	if err != nil {
		return 0
	}
	return id
}

// deleteOutboxRow removes one outbox row; 0 (nothing persisted) is a no-op.
func (m *jobManager) deleteOutboxRow(id int64) {
	if id == 0 || m.rt == nil {
		return
	}
	_ = m.rt.outboxDelete(id)
}

// shutdownCancelled distinguishes a job cancelAll killed from one that
// finished on its own while the runtime closed.
func (m *jobManager) shutdownCancelled(jobID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	j := m.jobs[jobID]
	return j != nil && j.shutdownCancel
}

// putRunningMarker persists j's "running" row and records its id. Call
// without m.mu — this writes to SQLite; the id store takes the lock.
func (m *jobManager) putRunningMarker(j *bgJob, ownerID string) {
	if j.store == nil {
		return
	}
	mk := runningMarker{
		PID: os.Getpid(), Kind: j.kind.String(), JobID: j.id, Title: j.title,
		Agent: j.agent, CronJob: j.cronJob, OwnerID: ownerID,
		LogPath: j.logPath, ChildID: j.childID, StartedAt: j.startedAt,
	}
	b, err := json.Marshal(mk)
	if err != nil {
		return
	}
	id, err := j.store.OutboxPut("running", string(b))
	if err != nil {
		return
	}
	m.mu.Lock()
	j.markerID = id
	m.mu.Unlock()
}

// clearRunningMarker deletes j's row on finish, unless shutdown cancelled it —
// then the marker is the boot-time record that its result was lost.
func (m *jobManager) clearRunningMarker(j *bgJob) {
	m.mu.Lock()
	id := j.markerID
	keep := j.shutdownCancel
	j.markerID = 0
	m.mu.Unlock()
	if keep {
		return
	}
	if id != 0 && j.store != nil {
		_ = j.store.OutboxDelete(id)
	}
}

// rememberUndelivered queues a row whose post failed for retry. rowID 0 means
// nothing was persisted, so there is nothing to retry from.
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

// takeUndelivered snapshots and clears the set; a row whose retry fails again
// re-enters it through dispatchCompletion.
func (m *jobManager) takeUndelivered() map[int64]struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := m.undelivered
	m.undelivered = nil
	return ids
}

// RedeliverUndelivered retries events whose post failed while this process
// was running — a Telegram outage riding out a night. Same ordering as
// RecoverCompletions: re-dispatch first, which persists a fresh row and
// re-enters the retry set on another failure, then delete the stale one.
// Called periodically by the front-end host; a no-op when nothing failed.
func (rt *Runtime) RedeliverUndelivered() int {
	ids := rt.jobs.takeUndelivered()
	if len(ids) == 0 {
		return 0
	}
	rows, err := rt.outboxLoadAll()
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
			_ = rt.outboxDelete(r.ID)
			continue
		}
		rt.jobs.dispatchCompletion(ev)
		_ = rt.outboxDelete(r.ID)
		redelivered++
	}
	return redelivered
}

// pidAlive probes with signal 0. An unsupported platform errs toward "dead",
// which at worst redelivers a row its owner was about to delete.
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

// RecoverCompletions redelivers what a previous process left: undelivered
// events, and running markers of jobs a dead process never finished, reported
// as failures so the loss is visible. Call once at startup, after
// SetCompletionHost.
func (rt *Runtime) RecoverCompletions() int {
	rows, err := rt.outboxLoadAll()
	if err != nil {
		return 0
	}
	recovered := 0
	for _, r := range rows {
		switch r.Kind {
		case "event":
			var ev CompletionEvent
			if err := json.Unmarshal([]byte(r.JSON), &ev); err != nil {
				_ = rt.outboxDelete(r.ID)
				continue
			}
			// Redeliver FIRST, delete after: dispatchCompletion persists its
			// own fresh row, so a crash in between leaves at least one row for
			// the next boot. Deleting first opens a loss window.
			ev.Note = joinNote(ev.Note, "recovered after a shell3 restart — this completion was never delivered")
			rt.jobs.dispatchCompletion(ev)
			_ = rt.outboxDelete(r.ID)
			recovered++
		case "running":
			var mk runningMarker
			if err := json.Unmarshal([]byte(r.JSON), &mk); err != nil {
				_ = rt.outboxDelete(r.ID)
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
			// Same ordering: a crash mid-recovery re-reports rather than
			// losing the loss.
			rt.jobs.dispatchCompletion(ev)
			_ = rt.outboxDelete(r.ID)
			recovered++
		}
	}
	return recovered
}
