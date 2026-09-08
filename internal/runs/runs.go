package runs

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/weatherjean/shell3/internal/llm"
)

// NewSession mints an ID and creates its durable conversation record.
func (s *Store) NewSession() (string, error) {
	id := newID()
	if _, err := s.db.Exec(`INSERT INTO sessions (id) VALUES (?)`, id); err != nil {
		return "", fmt.Errorf("runs: new session: %w", err)
	}
	return id, nil
}

// AppendMessage appends one message at the next sequence number.
func (s *Store) AppendMessage(id string, m llm.Message) error {
	return s.AppendMessages(id, []llm.Message{m})
}

// AppendMessages commits a complete pending history suffix in one transaction.
func (s *Store) AppendMessages(id string, messages []llm.Message) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("runs: append message: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := appendMessages(tx, id, messages); err != nil {
		return err
	}
	return tx.Commit()
}

func appendMessages(tx *sql.Tx, id string, messages []llm.Message) error {
	var seq int
	if err := tx.QueryRow(
		`SELECT COALESCE(MAX(seq),-1)+1 FROM messages WHERE session_id=?`, id,
	).Scan(&seq); err != nil {
		return fmt.Errorf("runs: append message: %w", err)
	}
	for i, m := range messages {
		b, err := json.Marshal(m)
		if err != nil {
			return fmt.Errorf("runs: marshal message: %w", err)
		}
		if _, err := tx.Exec(`INSERT INTO messages (session_id, seq, json) VALUES (?,?,?)`, id, seq+i, string(b)); err != nil {
			return fmt.Errorf("runs: append message: %w", err)
		}
	}
	return nil
}

// RollSession persists both sides of compaction and moves existing surface
// markers atomically. Callers publish the new in-memory ID only after success.
func (s *Store) RollSession(previous string, pending, continuation []llm.Message) (string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	id := newID()
	if _, err := tx.Exec(`INSERT INTO sessions (id) VALUES (?)`, id); err != nil {
		return "", err
	}
	if err := appendMessages(tx, previous, pending); err != nil {
		return "", err
	}
	if err := appendMessages(tx, id, continuation); err != nil {
		return "", err
	}
	if _, err := tx.Exec(`UPDATE current_sessions SET session_id=? WHERE session_id=?`, id, previous); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return id, nil
}

// LoadMessages reads the session's messages in order.
func (s *Store) LoadMessages(id string) ([]llm.Message, error) {
	var exists int
	if err := s.db.QueryRow(`SELECT 1 FROM sessions WHERE id=?`, id).Scan(&exists); err != nil {
		return nil, fmt.Errorf("runs: load session %s: %w", id, err)
	}
	rows, err := s.db.Query(`SELECT json FROM messages WHERE session_id=? ORDER BY seq`, id)
	if err != nil {
		return nil, fmt.Errorf("runs: load messages %s: %w", id, err)
	}
	defer rows.Close()
	var out []llm.Message
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("runs: load messages %s: %w", id, err)
		}
		var m llm.Message
		if err := json.Unmarshal([]byte(raw), &m); err != nil {
			return nil, fmt.Errorf("runs: decode message in %s: %w", id, err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// EndSession removes an empty conversation. Stored messages and job logs stay
// available for the front end's durable session marker to resume.
func (s *Store) EndSession(id string) error {
	var hasMessages bool
	if err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM messages WHERE session_id=?)`, id).Scan(&hasMessages); err != nil {
		return fmt.Errorf("runs: inspect session %s: %w", id, err)
	}
	if hasMessages {
		return nil
	}
	dir := s.jobsDir(id)
	if dir == "" {
		return fmt.Errorf("runs: invalid session id %q", id)
	}
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("runs: inspect session logs: %w", err)
	}
	if len(entries) > 0 {
		return nil
	}
	return s.deleteSession(id)
}

// SetLastPromptTokens records the count a later resume restores its gauge from.
func (s *Store) SetLastPromptTokens(id string, n int) error {
	_, err := s.db.Exec(
		`UPDATE sessions SET last_prompt_tokens=? WHERE id=? AND last_prompt_tokens<>?`, n, id, n)
	if err != nil {
		return fmt.Errorf("runs: set last prompt tokens: %w", err)
	}
	return nil
}

// LastPromptTokens returns the persisted count, or zero when none is known.
func (s *Store) LastPromptTokens(id string) int {
	var n int
	if err := s.db.QueryRow(
		`SELECT last_prompt_tokens FROM sessions WHERE id=?`, id,
	).Scan(&n); err != nil {
		return 0
	}
	return n
}

// ReminderLine is one persisted system reminder for faithful replay.
type ReminderLine struct {
	Seq  int
	Text string
}

func (s *Store) AppendReminder(id string, seq int, text string) error {
	if _, err := s.db.Exec(
		`INSERT INTO reminders (session_id, seq, text) VALUES (?,?,?)`, id, seq, text,
	); err != nil {
		return fmt.Errorf("runs: append reminder: %w", err)
	}
	return nil
}

func (s *Store) LoadReminders(id string) ([]ReminderLine, error) {
	rows, err := s.db.Query(
		`SELECT seq, text FROM reminders WHERE session_id=? ORDER BY rowid`, id)
	if err != nil {
		return nil, fmt.Errorf("runs: load reminders %s: %w", id, err)
	}
	defer rows.Close()
	var out []ReminderLine
	for rows.Next() {
		var r ReminderLine
		if err := rows.Scan(&r.Seq, &r.Text); err != nil {
			return nil, fmt.Errorf("runs: load reminders %s: %w", id, err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) TruncateReminders(id string) error {
	if _, err := s.db.Exec(`DELETE FROM reminders WHERE session_id=?`, id); err != nil {
		return fmt.Errorf("runs: truncate reminders: %w", err)
	}
	return nil
}

func (s *Store) deleteSession(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("runs: delete session: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, q := range []string{
		`DELETE FROM messages WHERE session_id=?`,
		`DELETE FROM reminders WHERE session_id=?`,
		`DELETE FROM current_sessions WHERE session_id=?`,
		`DELETE FROM sessions WHERE id=?`,
	} {
		if _, err := tx.Exec(q, id); err != nil {
			return fmt.Errorf("runs: delete session %s: %w", id, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("runs: delete session: %w", err)
	}
	_ = os.RemoveAll(s.runDir(id))
	return nil
}

// JobLogPath is runs/<id>/jobs/<jobID>.log, with the directory created for
// the caller. Job logs stay plain files so completion notices can link them.
func (s *Store) JobLogPath(id, jobID string) string {
	if jobID == "" || jobID != filepath.Base(jobID) {
		return ""
	}
	dir := s.jobsDir(id)
	if dir == "" {
		return ""
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return ""
	}
	return filepath.Join(dir, jobID+".log")
}

func (s *Store) runDir(id string) string {
	if id == "" || id == "." || id == ".." || id != filepath.Base(id) {
		return ""
	}
	return filepath.Join(s.root, "runs", id)
}

func (s *Store) jobsDir(id string) string {
	d := s.runDir(id)
	if d == "" {
		return ""
	}
	return filepath.Join(d, "jobs")
}
