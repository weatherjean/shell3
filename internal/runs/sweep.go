package runs

import (
	"fmt"
	"time"
)

// emptyKeep is how long a session with no stored content survives before
// Sweep treats it as abandoned trash rather than a session some live process
// just opened.
const emptyKeep = time.Hour

// Sweep deletes sessions whose recency (last_at) is older than
// now.Add(-keep), plus empty trash sessions past their grace window, and
// drops thread entries pointing at sessions that no longer exist. keep<=0 is
// a deliberate "keep forever" (the shell3.yaml runs_keep_days: 0 case): no
// session with stored content is ever removed then.
//
// Returns the removed session ids and the number of stale thread entries
// dropped. Runs on its own connection so callers can sweep before the live
// store opens.
func Sweep(root string, keep time.Duration, now time.Time) (removedIDs []string, threadsDropped int, err error) {
	s, err := Open(root)
	if err != nil {
		return nil, 0, err
	}
	defer s.Close()

	var ids []string
	cutoff := encTime(now.Add(-keep))
	if keep > 0 {
		rows, err := s.db.Query(`SELECT id FROM sessions WHERE last_at < ?`, cutoff)
		if err != nil {
			return nil, 0, fmt.Errorf("runs: sweep: %w", err)
		}
		ids, err = scanIDs(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("runs: sweep: %w", err)
		}
	}
	// Trash: sessions with no messages and no job log, past the grace window —
	// crash leftovers EndSession never got to clean up. Removed regardless of
	// keep (keep<=0 preserves history forever, not trash).
	rows, err := s.db.Query(`SELECT id FROM sessions
		WHERE last_at < ? AND NOT EXISTS
			(SELECT 1 FROM messages WHERE messages.session_id = sessions.id)`,
		encTime(now.Add(-emptyKeep)))
	if err != nil {
		return nil, 0, fmt.Errorf("runs: sweep: %w", err)
	}
	trash, err := scanIDs(rows)
	if err != nil {
		return nil, 0, fmt.Errorf("runs: sweep: %w", err)
	}
	seen := map[string]bool{}
	for _, id := range ids {
		seen[id] = true
	}
	for _, id := range trash {
		if !seen[id] && !hasJobLogs(s.jobsDir(id)) {
			ids = append(ids, id)
		}
	}

	// Count the thread entries the deletions are about to take with them, so
	// the janitor line reports every dropped entry, not just orphans.
	for _, id := range ids {
		var n int
		if err := s.db.QueryRow(
			`SELECT COUNT(*) FROM threads WHERE session_id=?`, id).Scan(&n); err == nil {
			threadsDropped += n
		}
	}
	if len(ids) > 0 {
		if err := s.deleteSessions(ids); err != nil {
			return nil, 0, err
		}
	}

	// Stale thread entries: sessions gone by any other means (the database
	// deleted and recreated around them, ids from another era).
	res, err := s.db.Exec(
		`DELETE FROM threads WHERE session_id NOT IN (SELECT id FROM sessions)`)
	if err != nil {
		return ids, threadsDropped, fmt.Errorf("runs: sweep threads: %w", err)
	}
	n, _ := res.RowsAffected()
	return ids, threadsDropped + int(n), nil
}

func scanIDs(rows interface {
	Next() bool
	Scan(...any) error
	Close() error
	Err() error
}) ([]string, error) {
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
