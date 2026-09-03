package runs

import (
	"errors"
	"fmt"
	"time"
)

// ErrScheduleOverlap means a skip-overlap schedule already has a running
// invocation. It is an expected clock event, not an execution failure.
var ErrScheduleOverlap = errors.New("schedule already has a running invocation")

// ScheduleRun is one durable scheduled wrk invocation. The output remains a
// file in the wrk run directory; SQLite stores only its inspectable pointer.
type ScheduleRun struct {
	ID          string    `json:"id"`
	Schedule    string    `json:"schedule"`
	Task        string    `json:"task"`
	Trigger     string    `json:"trigger"`
	RunDir      string    `json:"run_dir"`
	OutputPath  string    `json:"output_path"`
	ScheduledAt time.Time `json:"scheduled_at"`
	StartedAt   time.Time `json:"started_at"`
	FinishedAt  time.Time `json:"finished_at,omitzero"`
	Status      string    `json:"status"`
	Error       string    `json:"error,omitempty"`
}

// StartScheduleRun inserts a running record. For overlap=skip, the insert and
// running-row check are one SQLite statement, so separate shell3 processes
// cannot both admit the same schedule after racing a read.
func (s *Store) StartScheduleRun(run ScheduleRun, overlap string) error {
	if run.ID == "" || run.Schedule == "" || run.Task == "" || run.Trigger == "" || run.RunDir == "" || run.OutputPath == "" {
		return errors.New("runs: incomplete schedule run")
	}
	if overlap != "skip" && overlap != "allow" {
		return fmt.Errorf("runs: invalid schedule overlap %q", overlap)
	}
	if run.ScheduledAt.IsZero() {
		run.ScheduledAt = time.Now().UTC()
	}
	if run.StartedAt.IsZero() {
		run.StartedAt = time.Now().UTC()
	}
	res, err := s.db.Exec(`INSERT INTO schedule_runs
		(id, schedule_name, task, trigger, run_dir, output_path, scheduled_at, started_at, status)
		SELECT ?,?,?,?,?,?,?,?,'running'
		WHERE ?='allow' OR NOT EXISTS (
			SELECT 1 FROM schedule_runs WHERE schedule_name=? AND status='running'
		)`, run.ID, run.Schedule, run.Task, run.Trigger, run.RunDir, run.OutputPath,
		encTime(run.ScheduledAt), encTime(run.StartedAt), overlap, run.Schedule)
	if err != nil {
		return fmt.Errorf("runs: start schedule run: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("runs: start schedule run: %w", err)
	} else if n == 0 {
		return ErrScheduleOverlap
	}
	return nil
}

// FinishScheduleRun records one terminal transition. Repeated completion
// delivery is harmless: only a running row can move to a terminal state.
func (s *Store) FinishScheduleRun(id, status, failure string) error {
	if status != "done" && status != "failed" {
		return fmt.Errorf("runs: invalid terminal schedule status %q", status)
	}
	res, err := s.db.Exec(`UPDATE schedule_runs
		SET status=?, error=?, finished_at=? WHERE id=? AND status='running'`,
		status, failure, encTime(time.Now()), id)
	if err != nil {
		return fmt.Errorf("runs: finish schedule run: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("runs: finish schedule run: %w", err)
	} else if n == 0 {
		var existing string
		if err := s.db.QueryRow(`SELECT status FROM schedule_runs WHERE id=?`, id).Scan(&existing); err != nil {
			return fmt.Errorf("runs: finish unknown schedule run %q", id)
		}
		if existing != status {
			return fmt.Errorf("runs: schedule run %q is already %s", id, existing)
		}
	}
	return nil
}

// ListScheduleRuns returns newest-first schedule history. Empty filters match
// every value; a non-positive limit means no limit.
func (s *Store) ListScheduleRuns(schedule, status string, limit int) ([]ScheduleRun, error) {
	query := `SELECT id, schedule_name, task, trigger, run_dir, output_path,
		scheduled_at, started_at, finished_at, status, error
		FROM schedule_runs WHERE (?='' OR schedule_name=?) AND (?='' OR status=?)
		ORDER BY started_at DESC`
	args := []any{schedule, schedule, status, status}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("runs: list schedule runs: %w", err)
	}
	defer rows.Close()
	var out []ScheduleRun
	for rows.Next() {
		var run ScheduleRun
		var scheduled, started, finished string
		if err := rows.Scan(&run.ID, &run.Schedule, &run.Task, &run.Trigger, &run.RunDir,
			&run.OutputPath, &scheduled, &started, &finished, &run.Status, &run.Error); err != nil {
			return nil, fmt.Errorf("runs: scan schedule run: %w", err)
		}
		run.ScheduledAt, run.StartedAt, run.FinishedAt = decTime(scheduled), decTime(started), decTime(finished)
		out = append(out, run)
	}
	return out, rows.Err()
}

func (s *Store) RunningScheduleRuns() ([]ScheduleRun, error) {
	return s.ListScheduleRuns("", "running", 0)
}
