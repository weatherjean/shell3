package runs

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/weatherjean/shell3/internal/llm"
)

// Meta is the per-session metadata (one row in the sessions table).
type Meta struct {
	ID        string    `json:"id"`
	Workdir   string    `json:"workdir"`
	ConfigDir string    `json:"config_dir"`
	Model     string    `json:"model"`
	Status    string    `json:"status"` // "live" | "ended"
	ParentID  string    `json:"parent_id,omitempty"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at,omitzero"`
	LastAt    time.Time `json:"last_at"`
	// LastPromptTokens is the provider-reported prompt-token count from the most
	// recent turn. Persisted so a resume restores the accurate context gauge
	// instead of re-deriving it with the chars/4 estimate (which underestimates
	// token-dense content, letting the prompt overflow the model window without
	// ever tripping prune/compaction). Zero on old sessions written before this
	// field existed; resume then falls back to the estimate.
	LastPromptTokens int `json:"last_prompt_tokens,omitempty"`
}

// NewSession mints an ID, inserts the session row, and returns the ID.
func (s *Store) NewSession(m Meta) (string, error) {
	id := newID()
	now := time.Now().UTC()
	_, err := s.db.Exec(`INSERT INTO sessions
		(id, workdir, config_dir, model, status, parent_id, started_at, last_at, last_prompt_tokens)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		id, m.Workdir, m.ConfigDir, m.Model, "live", m.ParentID,
		encTime(now), encTime(now), m.LastPromptTokens)
	if err != nil {
		return "", fmt.Errorf("runs: new session: %w", err)
	}
	return id, nil
}

// AppendMessage appends one message to the session, bumps its recency, and
// indexes any searchable text. One transaction; crash-safe by construction.
func (s *Store) AppendMessage(id string, m llm.Message) error {
	b, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("runs: marshal message: %w", err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("runs: append message: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	var seq int
	if err := tx.QueryRow(
		`SELECT COALESCE(MAX(seq),-1)+1 FROM messages WHERE session_id=?`, id,
	).Scan(&seq); err != nil {
		return fmt.Errorf("runs: append message: %w", err)
	}
	if _, err := tx.Exec(
		`INSERT INTO messages (session_id, seq, json) VALUES (?,?,?)`, id, seq, string(b),
	); err != nil {
		return fmt.Errorf("runs: append message: %w", err)
	}
	if text := searchableText(m); text != "" {
		if _, err := tx.Exec(
			`INSERT INTO messages_fts (text, session_id, seq, role) VALUES (?,?,?,?)`,
			text, id, seq, string(m.Role),
		); err != nil {
			return fmt.Errorf("runs: index message: %w", err)
		}
	}
	if _, err := tx.Exec(
		`UPDATE sessions SET last_at=? WHERE id=?`, encTime(time.Now()), id,
	); err != nil {
		return fmt.Errorf("runs: append message: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("runs: append message: %w", err)
	}
	return nil
}

// LoadMessages reads the session's messages in order.
func (s *Store) LoadMessages(id string) ([]llm.Message, error) {
	rows, err := s.db.Query(
		`SELECT json FROM messages WHERE session_id=? ORDER BY seq`, id)
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

// EndSession marks the session ended. A session that stored nothing — no
// message, no job log — leaves no trace: its row is deleted instead, so the
// pinned cron dispatch parent and aborted front-end sessions don't litter the
// store.
func (s *Store) EndSession(id string) error {
	if !s.HasMessages(id) && !hasJobLogs(s.jobsDir(id)) {
		return s.deleteSessions([]string{id})
	}
	now := encTime(time.Now())
	_, err := s.db.Exec(
		`UPDATE sessions SET status='ended', ended_at=?, last_at=? WHERE id=?`, now, now, id)
	if err != nil {
		return fmt.Errorf("runs: end session: %w", err)
	}
	return nil
}

// HasMessages reports whether the session has stored at least one message —
// the cheap "worth listing/replaying" probe.
// SessionExists reports whether a session row with this id is stored.
func (s *Store) SessionExists(id string) bool {
	var one int
	err := s.db.QueryRow(
		`SELECT 1 FROM sessions WHERE id=? LIMIT 1`, id).Scan(&one)
	return err == nil
}

func (s *Store) HasMessages(id string) bool {
	var one int
	err := s.db.QueryRow(
		`SELECT 1 FROM messages WHERE session_id=? LIMIT 1`, id).Scan(&one)
	return err == nil
}

// SetLastPromptTokens records the provider-reported prompt-token count for the
// session (see Meta.LastPromptTokens) so a later resume restores the accurate
// context gauge.
func (s *Store) SetLastPromptTokens(id string, n int) error {
	_, err := s.db.Exec(
		`UPDATE sessions SET last_prompt_tokens=? WHERE id=? AND last_prompt_tokens<>?`, n, id, n)
	if err != nil {
		return fmt.Errorf("runs: set last prompt tokens: %w", err)
	}
	return nil
}

// LastPromptTokens returns the persisted provider-reported prompt-token count
// for the session, or 0 when the session is unknown or predates the field.
// Callers treat 0 as "no persisted value" and fall back to the estimate.
func (s *Store) LastPromptTokens(id string) int {
	var n int
	if err := s.db.QueryRow(
		`SELECT last_prompt_tokens FROM sessions WHERE id=?`, id).Scan(&n); err != nil {
		return 0
	}
	return n
}

// TouchSession bumps LastAt. Unknown session ids error.
func (s *Store) TouchSession(id string) error {
	res, err := s.db.Exec(
		`UPDATE sessions SET last_at=? WHERE id=?`, encTime(time.Now()), id)
	if err != nil {
		return fmt.Errorf("runs: touch session: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("runs: touch session: unknown session %q", id)
	}
	return nil
}

// ListSessions returns metas newest-first (by ID, which sorts chronologically).
func (s *Store) ListSessions(limit int) ([]Meta, error) {
	q := `SELECT id, workdir, config_dir, model, status, parent_id,
		started_at, ended_at, last_at, last_prompt_tokens
		FROM sessions ORDER BY id DESC`
	var rows *sql.Rows
	var err error
	if limit > 0 {
		rows, err = s.db.Query(q+` LIMIT ?`, limit)
	} else {
		rows, err = s.db.Query(q)
	}
	if err != nil {
		return nil, fmt.Errorf("runs: list: %w", err)
	}
	defer rows.Close()
	var out []Meta
	for rows.Next() {
		var m Meta
		var started, ended, last string
		if err := rows.Scan(&m.ID, &m.Workdir, &m.ConfigDir, &m.Model, &m.Status,
			&m.ParentID, &started, &ended, &last, &m.LastPromptTokens); err != nil {
			return nil, fmt.Errorf("runs: list: %w", err)
		}
		m.StartedAt, m.EndedAt, m.LastAt = decTime(started), decTime(ended), decTime(last)
		out = append(out, m)
	}
	return out, rows.Err()
}

// ReminderLine is one persisted system-reminder, anchored to the message index
// it precedes (mirrors chat.ReminderRecord) for faithful session replay.
type ReminderLine struct {
	Seq  int    `json:"seq"`
	Text string `json:"text"`
}

// AppendReminder stores one reminder for the session.
func (s *Store) AppendReminder(id string, seq int, text string) error {
	if _, err := s.db.Exec(
		`INSERT INTO reminders (session_id, seq, text) VALUES (?,?,?)`, id, seq, text); err != nil {
		return fmt.Errorf("runs: append reminder: %w", err)
	}
	return nil
}

// LoadReminders reads the session's reminders in insertion order.
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

// TruncateReminders removes the session's reminders, for a session whose
// history has been replaced (see chat.Session.SetMessages).
func (s *Store) TruncateReminders(id string) error {
	if _, err := s.db.Exec(`DELETE FROM reminders WHERE session_id=?`, id); err != nil {
		return fmt.Errorf("runs: truncate reminders: %w", err)
	}
	return nil
}

// Transcript returns the session's messages as a JSONL blob (one message per
// line — the wire format ParseMessages reads), or "" when the session is
// unknown or empty. Used by jobManager.transcript to surface a child
// session's message log after completion.
func (s *Store) Transcript(id string) string {
	rows, err := s.db.Query(
		`SELECT json FROM messages WHERE session_id=? ORDER BY seq`, id)
	if err != nil {
		return ""
	}
	defer rows.Close()
	var b strings.Builder
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return ""
		}
		b.WriteString(raw)
		b.WriteByte('\n')
	}
	return b.String()
}

// LatestSession returns the newest top-level session ID matching
// workdir+configDir. Subagent child sessions are skipped: they share the
// parent's workdir+config and sort newer, and resume-latest must only ever
// rejoin a top-level conversation.
func (s *Store) LatestSession(workdir, configDir string) (string, bool, error) {
	var id string
	err := s.db.QueryRow(`SELECT id FROM sessions
		WHERE parent_id='' AND workdir=? AND config_dir=?
		ORDER BY id DESC LIMIT 1`, workdir, configDir).Scan(&id)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("runs: latest session: %w", err)
	}
	return id, true, nil
}

// deleteSessions removes the given sessions' rows (messages, reminders,
// thread entries, FTS entries) and their on-disk job-log dirs.
func (s *Store) deleteSessions(ids []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("runs: delete sessions: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, id := range ids {
		for _, q := range []string{
			`DELETE FROM messages WHERE session_id=?`,
			`DELETE FROM messages_fts WHERE session_id=?`,
			`DELETE FROM reminders WHERE session_id=?`,
			`DELETE FROM threads WHERE session_id=?`,
			`DELETE FROM sessions WHERE id=?`,
		} {
			if _, err := tx.Exec(q, id); err != nil {
				return fmt.Errorf("runs: delete session %s: %w", id, err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("runs: delete sessions: %w", err)
	}
	for _, id := range ids {
		_ = os.RemoveAll(s.runDir(id)) // best-effort; job logs are disposable
	}
	return nil
}

// JobLogPath returns the on-disk log path for a background job owned by
// session id — runs/<id>/jobs/<jobID>.log — creating the dir so the caller
// can open the file directly. Job logs stay plain files (not database rows)
// so the notifier's read tool can open them by path. Returns "" when the dir
// cannot be created (the caller then simply keeps no on-disk log).
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

// runDir resolves a session's on-disk directory (job logs only — the
// conversation lives in the database). IDs arrive from user-controlled
// surfaces, so anything that is not a plain path component is rejected —
// "../../../etc" must never escape the store.
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

func hasJobLogs(dir string) bool {
	if dir == "" {
		return false
	}
	ents, err := os.ReadDir(dir)
	return err == nil && len(ents) > 0
}
