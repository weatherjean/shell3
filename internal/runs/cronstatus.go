package runs

// cron_status is cron's own table (see schemaVersion in db.go): one
// row per job name, holding an opaque JSON blob the cron package encodes and
// decodes. This package stores bytes and does not parse them — cron.JobStatus
// lives in internal/cron, and this package must not import it back.
//
// Unlike threads, this table is NEVER touched by runs.Sweep: a job's history
// has no session to expire with, and nothing here names a session id for the
// sweep's "orphaned thread" check to (wrongly) prune against.

// CronStatusSave writes (or replaces) one job's status blob.
func (s *Store) CronStatusSave(name, json string) error {
	_, err := s.db.Exec(`INSERT INTO cron_status (name, json) VALUES (?,?)
		ON CONFLICT (name) DO UPDATE SET json=excluded.json`, name, json)
	return err
}

// CronStatusLoadAll returns every persisted job's status blob, keyed by name.
func (s *Store) CronStatusLoadAll() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT name, json FROM cron_status`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]string{}
	for rows.Next() {
		var name, json string
		if err := rows.Scan(&name, &json); err != nil {
			return nil, err
		}
		out[name] = json
	}
	return out, rows.Err()
}
