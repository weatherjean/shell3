package runs

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// emptyKeep is how long a session with no stored content survives before
// Sweep treats it as abandoned trash rather than a session some live process
// just opened.
const emptyKeep = time.Hour

// Sweep deletes sessions whose recency (last_at) is older than
// now.Add(-keep), plus empty trash sessions past their grace window, and
// drops thread entries pointing at sessions that no longer exist. keep<=0 is
// a deliberate "keep forever" (the runs_keep_days: 0 case): no
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
	// keep (keep<=0 preserves history forever, not trash). Dispatch parents
	// are spared: the pinned cron parent runs no turns, so it is always
	// message-less — deleting it would dangle its children's parent_id.
	rows, err := s.db.Query(`SELECT id FROM sessions
		WHERE last_at < ? AND NOT EXISTS
			(SELECT 1 FROM messages WHERE messages.session_id = sessions.id)
		AND NOT EXISTS
			(SELECT 1 FROM sessions c WHERE c.parent_id = sessions.id)`,
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

	// Stale "live" rows: Sweep runs at process start, when nothing from a
	// previous run can still be live — flip leftovers past the grace window
	// to ended (recent ones may belong to a concurrent `shell3 ask`).
	if _, err := s.db.Exec(
		`UPDATE sessions SET status='ended', ended_at=last_at
		 WHERE status='live' AND last_at < ?`,
		encTime(now.Add(-emptyKeep))); err != nil {
		return ids, threadsDropped, fmt.Errorf("runs: sweep live: %w", err)
	}

	// Stale thread entries: sessions gone by any other means (the database
	// deleted and recreated around them, ids from another era).
	res, err := s.db.Exec(
		`DELETE FROM threads WHERE session_id NOT IN (SELECT id FROM sessions)`)
	if err != nil {
		return ids, threadsDropped, fmt.Errorf("runs: sweep threads: %w", err)
	}
	n, _ := res.RowsAffected()

	s.sweepOrphanDirs(now)
	return ids, threadsDropped + int(n), nil
}

// sweepOrphanDirs removes runs/<id>/ directories with no session row: crash
// debris. Grace-windowed so a dir a
// live process created moments ago (a job log ahead of its first write)
// survives; best-effort per dir.
func (s *Store) sweepOrphanDirs(now time.Time) {
	dir := filepath.Join(s.root, "runs")
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		var one int
		if err := s.db.QueryRow(
			`SELECT 1 FROM sessions WHERE id=?`, e.Name()).Scan(&one); err == nil {
			continue // live session's job-log dir
		}
		if info, err := e.Info(); err != nil || info.ModTime().After(now.Add(-emptyKeep)) {
			continue
		}
		_ = os.RemoveAll(filepath.Join(dir, e.Name()))
	}
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
