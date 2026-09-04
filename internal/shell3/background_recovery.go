package shell3

import (
	"fmt"
	"os"
	"strings"
	"syscall"

	"github.com/weatherjean/shell3/internal/inbox"
	"github.com/weatherjean/shell3/internal/runs"
)

// putRunningMarker persists j before its process starts. The row is deleted
// only after the completion notice reaches the durable filesystem inbox.
func (m *jobManager) putRunningMarker(j *bgJob, ownerID string) {
	if j.store == nil {
		return
	}
	id, err := j.store.BackgroundJobPut(runs.BackgroundJob{
		PID: os.Getpid(), JobID: j.id, Title: j.title, OwnerID: ownerID,
		LogPath: j.logPath, StartedAt: j.startedAt,
	})
	if err != nil {
		if m.rt != nil {
			m.rt.Logger().Warn("background job marker persistence failed", "job", j.id, "error", err)
		}
		return
	}
	m.mu.Lock()
	j.markerID = id
	m.mu.Unlock()
}

func (m *jobManager) deleteRunningMarker(id int64, store *runs.Store, jobID string) {
	if id == 0 || store == nil {
		return
	}
	if err := store.BackgroundJobDelete(id); err != nil && m.rt != nil {
		m.rt.Logger().Warn("background job marker delete failed", "job", jobID, "row", id, "error", err)
	}
}

func (m *jobManager) clearRunningMarker(j *bgJob) {
	m.mu.Lock()
	id := j.markerID
	j.markerID = 0
	m.mu.Unlock()
	m.deleteRunningMarker(id, j.store, j.id)
}

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

// RecoverBackgroundJobs converts markers left by dead processes into durable
// failed notices. It writes the notice before deleting the marker, so a crash
// can duplicate a report but cannot lose it.
func (rt *Runtime) RecoverBackgroundJobs() int {
	rt.mu.Lock()
	store := rt.store
	rt.mu.Unlock()
	if store == nil {
		return 0
	}
	jobs, err := store.BackgroundJobs()
	if err != nil {
		rt.Logger().Warn("background job recovery failed", "error", err)
		return 0
	}
	recovered := 0
	for _, job := range jobs {
		if pidAlive(job.PID) {
			continue
		}
		var body strings.Builder
		fmt.Fprintf(&body, "background job %s failed: shell3 stopped while it was running; its final result was lost.\ncommand: %s", job.JobID, job.Title)
		if job.LogPath != "" {
			fmt.Fprintf(&body, "\npartial output: %s", job.LogPath)
		}
		_, err := rt.mainInbox().Notify(inbox.Request{
			To: "main", Source: "bash_bg:" + job.JobID, Event: "bash_bg.failed",
			Correlation: job.JobID, Body: body.String(),
		})
		if err != nil {
			rt.Logger().Warn("recovered background completion persistence failed", "job", job.JobID, "error", err)
			continue
		}
		if err := store.BackgroundJobDelete(job.ID); err != nil {
			rt.Logger().Warn("recovered background marker delete failed", "job", job.JobID, "row", job.ID, "error", err)
			continue
		}
		recovered++
	}
	return recovered
}
