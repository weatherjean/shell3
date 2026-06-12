package store

import (
	"database/sql"
	"fmt"
	"time"
)

// StartSessionWithParent inserts a new session row whose parent_session_id
// records the report pointer (who this session reports to on completion).
func (s *Store) StartSessionWithParent(parent int64, projectUUID, workdir string) (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.Exec(
		`INSERT INTO sessions(started_at, parent_session_id, project_uuid, workdir) VALUES(?, ?, ?, ?)`,
		now, parent, projectUUID, workdir)
	if err != nil {
		return 0, fmt.Errorf("store: start session with parent: %w", err)
	}
	return res.LastInsertId()
}

// ParentSessionID returns the report pointer for a session, or 0 if it is a
// root (NULL parent) or not found.
func (s *Store) ParentSessionID(id int64) (int64, error) {
	var p sql.NullInt64
	err := s.db.QueryRow(`SELECT parent_session_id FROM sessions WHERE id = ?`, id).Scan(&p)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("store: parent session id %d: %w", id, err)
	}
	if !p.Valid {
		return 0, nil
	}
	return p.Int64, nil
}

// SetLiveness records the current process whereabouts of a session. status is
// "live", "dormant", or "reviving". pid/sock are meaningful only when live.
func (s *Store) SetLiveness(id int64, pid int, sock, status string) error {
	if _, err := s.db.Exec(
		`UPDATE sessions SET pid = ?, sock = ?, status = ? WHERE id = ?`,
		pid, sock, status, id); err != nil {
		return fmt.Errorf("store: set liveness %d: %w", id, err)
	}
	return nil
}

// Liveness reads a session's current pid/sock/status.
func (s *Store) Liveness(id int64) (pid int, sock, status string, err error) {
	err = s.db.QueryRow(`SELECT pid, sock, status FROM sessions WHERE id = ?`, id).
		Scan(&pid, &sock, &status)
	if err == sql.ErrNoRows {
		return 0, "", "dormant", nil
	}
	if err != nil {
		return 0, "", "", fmt.Errorf("store: liveness %d: %w", id, err)
	}
	return pid, sock, status, nil
}

// ClaimRevive atomically transitions a session from "dormant" to "reviving",
// returning true only for the single caller that won the race. Losers (status
// already "reviving" or "live") get false. This is the leader election that
// ensures exactly one reviver process spawns for a dormant parent.
func (s *Store) ClaimRevive(id int64) (bool, error) {
	res, err := s.db.Exec(
		`UPDATE sessions SET status = 'reviving' WHERE id = ? AND status = 'dormant'`, id)
	if err != nil {
		return false, fmt.Errorf("store: claim revive %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("store: claim revive rows %d: %w", id, err)
	}
	return n == 1, nil
}

// AppendInbox parks a notification payload for a session to consume when it
// (re)boots. Atomic single-row insert; no coordination needed across writers.
func (s *Store) AppendInbox(id int64, payload []byte) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := s.db.Exec(
		`INSERT INTO inbox(session_id, payload_json, created_at) VALUES(?,?,?)`,
		id, string(payload), now); err != nil {
		return fmt.Errorf("store: append inbox %d: %w", id, err)
	}
	return nil
}

// DrainInbox returns and deletes all parked payloads for a session, oldest
// first. Destructive: a second call returns nothing.
func (s *Store) DrainInbox(id int64) ([][]byte, error) {
	rows, err := s.db.Query(
		`SELECT seq, payload_json FROM inbox WHERE session_id = ? ORDER BY seq ASC`, id)
	if err != nil {
		return nil, fmt.Errorf("store: drain inbox %d: %w", id, err)
	}
	defer rows.Close()
	var out [][]byte
	var maxSeq int64
	for rows.Next() {
		var seq int64
		var payload string
		if err := rows.Scan(&seq, &payload); err != nil {
			return nil, fmt.Errorf("store: drain inbox: scan: %w", err)
		}
		out = append(out, []byte(payload))
		maxSeq = seq
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) > 0 {
		if _, err := s.db.Exec(
			`DELETE FROM inbox WHERE session_id = ? AND seq <= ?`, id, maxSeq); err != nil {
			return nil, fmt.Errorf("store: drain inbox delete: %w", err)
		}
	}
	return out, nil
}
