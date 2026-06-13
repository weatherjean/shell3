package store

import (
	"fmt"
	"time"

	"github.com/weatherjean/shell3/internal/proc"
)

// Job is one tracked background process (the bash_bg / subagent / revive registry).
type Job struct {
	ID        string
	PID       int
	Cmd       string
	Log       string
	Workdir   string
	SessionID int64
	StartedAt time.Time
}

// AddJob records a spawned background process.
func (s *Store) AddJob(j Job) error {
	now := j.StartedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO jobs(id, pid, cmd, log, workdir, session_id, started_at)
		 VALUES(?,?,?,?,?,?,?)`,
		j.ID, j.PID, j.Cmd, j.Log, j.Workdir, j.SessionID, now.Format(time.RFC3339))
	if err != nil {
		return fmt.Errorf("store: add job %s: %w", j.ID, err)
	}
	return nil
}

// ListJobs returns tracked jobs for workdir (newest first), AFTER pruning any
// whose process has died — the registry is self-cleaning. limit<=0 → 50.
func (s *Store) ListJobs(workdir string, limit, offset int) ([]Job, error) {
	if err := s.pruneDeadJobs(workdir); err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(
		`SELECT id, pid, cmd, log, workdir, session_id, started_at
		 FROM jobs WHERE workdir = ? ORDER BY started_at DESC LIMIT ? OFFSET ?`,
		workdir, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("store: list jobs: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []Job
	for rows.Next() {
		var j Job
		var started string
		if err := rows.Scan(&j.ID, &j.PID, &j.Cmd, &j.Log, &j.Workdir, &j.SessionID, &started); err != nil {
			return nil, fmt.Errorf("store: list jobs: scan: %w", err)
		}
		j.StartedAt = parseRFC3339(started)
		out = append(out, j)
	}
	return out, rows.Err()
}

// pruneDeadJobs deletes rows for workdir whose pid is no longer running.
func (s *Store) pruneDeadJobs(workdir string) error {
	rows, err := s.db.Query(`SELECT id, pid FROM jobs WHERE workdir = ?`, workdir)
	if err != nil {
		return fmt.Errorf("store: prune jobs scan: %w", err)
	}
	var deadIDs []string
	for rows.Next() {
		var id string
		var pid int
		if err := rows.Scan(&id, &pid); err != nil {
			_ = rows.Close()
			return fmt.Errorf("store: prune jobs scan row: %w", err)
		}
		if !proc.Alive(pid) {
			deadIDs = append(deadIDs, id)
		}
	}
	_ = rows.Close()
	for _, id := range deadIDs {
		if _, err := s.db.Exec(`DELETE FROM jobs WHERE id = ?`, id); err != nil {
			return fmt.Errorf("store: prune job %s: %w", id, err)
		}
	}
	return nil
}

// ClearJobs removes all jobs for workdir, returning the count removed. Used by
// KillAll after it has signalled the pids.
func (s *Store) ClearJobs(workdir string) (int, error) {
	res, err := s.db.Exec(`DELETE FROM jobs WHERE workdir = ?`, workdir)
	if err != nil {
		return 0, fmt.Errorf("store: clear jobs: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}
