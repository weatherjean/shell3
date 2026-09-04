package runs

import "time"

// BackgroundJob is the crash marker for one managed command. Completed
// results live in the filesystem inbox; this row exists only while the process
// may still be running.
type BackgroundJob struct {
	ID        int64
	PID       int
	JobID     string
	Title     string
	OwnerID   string
	LogPath   string
	StartedAt time.Time
}

// BackgroundJobPut records a running command and returns its row id.
func (s *Store) BackgroundJobPut(job BackgroundJob) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO background_jobs (pid, job_id, title, owner_id, log_path, started_at)
		VALUES (?, ?, ?, ?, ?, ?)`, job.PID, job.JobID, job.Title, job.OwnerID, job.LogPath, job.StartedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// BackgroundJobDelete removes one marker; deleting an absent id is harmless.
func (s *Store) BackgroundJobDelete(id int64) error {
	_, err := s.db.Exec(`DELETE FROM background_jobs WHERE id = ?`, id)
	return err
}

// BackgroundJobs returns every running marker in insertion order.
func (s *Store) BackgroundJobs() ([]BackgroundJob, error) {
	rows, err := s.db.Query(`SELECT id, pid, job_id, title, owner_id, log_path, started_at
		FROM background_jobs ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []BackgroundJob
	for rows.Next() {
		var job BackgroundJob
		var started string
		if err := rows.Scan(&job.ID, &job.PID, &job.JobID, &job.Title, &job.OwnerID, &job.LogPath, &started); err != nil {
			return nil, err
		}
		var err error
		job.StartedAt, err = time.Parse(time.RFC3339Nano, started)
		if err != nil {
			return nil, err
		}
		out = append(out, job)
	}
	return out, rows.Err()
}
