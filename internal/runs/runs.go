package runs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/weatherjean/shell3/internal/llm"
)

// Meta is the per-session metadata (one row in the sessions table).
type Meta struct {
	ID        string `json:"id"`
	Workdir   string `json:"workdir"`
	ConfigDir string `json:"config_dir"`
	Model     string `json:"model"`
	Status    string `json:"status"` // "live" | "ended"
	ParentID  string `json:"parent_id,omitempty"`
	// Agent is who ran this session — what makes an employee's runs findable
	// by name rather than only by id.
	Agent string `json:"agent,omitempty"`
	// CronJob is what started this session, "" for a front-end or task-tool
	// one — what makes "what did this job do" answerable without guessing.
	CronJob   string    `json:"cron_job,omitempty"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   time.Time `json:"ended_at,omitzero"`
	LastAt    time.Time `json:"last_at"`
	// LastPromptTokens is the latest turn's provider-reported count, persisted
	// so a resume restores the accurate gauge rather than the chars/4
	// estimate, which under-counts token-dense content and lets the prompt
	// overflow the window without ever tripping compaction. Zero falls back.
	LastPromptTokens int `json:"last_prompt_tokens,omitempty"`
	// TotalPromptTokens and TotalCompletionTokens are the cumulative ledger,
	// unlike LastPromptTokens, which is overwritten each turn and gauges how
	// full the context is now. These only grow, and answer what a session
	// cost — the question CronRollup totals per job.
	TotalPromptTokens     int64 `json:"total_prompt_tokens,omitempty"`
	TotalCompletionTokens int64 `json:"total_completion_tokens,omitempty"`
}

// NewSession mints an ID, inserts the session row, and returns the ID.
func (s *Store) NewSession(m Meta) (string, error) {
	id := newID()
	now := time.Now().UTC()
	_, err := s.db.Exec(`INSERT INTO sessions
		(id, workdir, config_dir, model, status, parent_id, agent, cron_job, started_at, last_at, last_prompt_tokens)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		id, m.Workdir, m.ConfigDir, m.Model, "live", m.ParentID, m.Agent, m.CronJob,
		encTime(now), encTime(now), m.LastPromptTokens)
	if err != nil {
		return "", fmt.Errorf("runs: new session: %w", err)
	}
	return id, nil
}

// AppendMessage appends a message, bumps recency and indexes searchable text
// in one transaction.
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
	// One clock reading for both, so a message can never look newer than the
	// session holding it.
	now := time.Now()
	if _, err := tx.Exec(
		`INSERT INTO messages (session_id, seq, json, ts) VALUES (?,?,?,?)`,
		id, seq, string(b), encTime(now),
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
		`UPDATE sessions SET last_at=? WHERE id=?`, encTime(now), id,
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

// EndSession marks the session ended, or deletes its row when it stored
// nothing at all, so the pinned cron parent and aborted sessions leave no
// trace.
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

// HasMessages is the cheap "worth listing or replaying" probe.
func (s *Store) HasMessages(id string) bool {
	var one int
	err := s.db.QueryRow(
		`SELECT 1 FROM messages WHERE session_id=? LIMIT 1`, id).Scan(&one)
	return err == nil
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

// LastPromptTokens is the persisted count, 0 for an unknown session. Callers
// treat 0 as "no persisted value" and fall back to the estimate.
func (s *Store) LastPromptTokens(id string) int {
	var n int
	if err := s.db.QueryRow(
		`SELECT last_prompt_tokens FROM sessions WHERE id=?`, id).Scan(&n); err != nil {
		return 0
	}
	return n
}

// AddUsage accumulates one turn onto the session's cumulative ledger. Where
// LastPromptTokens gauges context fullness, this answers what the session
// cost — the question an operator running unattended cron work needs. An
// unknown id errors rather than no-ops: a cost that lands nowhere is worse
// than a loud failure naming the id.
func (s *Store) AddUsage(id string, prompt, completion int) error {
	res, err := s.db.Exec(`UPDATE sessions
		SET total_prompt_tokens = total_prompt_tokens + ?,
		    total_completion_tokens = total_completion_tokens + ?
		WHERE id = ?`, prompt, completion, id)
	if err != nil {
		return fmt.Errorf("runs: add usage: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("runs: add usage: unknown session %q", id)
	}
	return nil
}

// metaColumns keeps the SELECT list and scanMeta's Scan from drifting apart.
const metaColumns = `id, workdir, config_dir, model, status, parent_id, agent, cron_job,
		started_at, ended_at, last_at, last_prompt_tokens,
		total_prompt_tokens, total_completion_tokens`

// scanMeta reads one metaColumns row into a Meta, decoding its timestamps.
func scanMeta(row interface{ Scan(...any) error }) (Meta, error) {
	var m Meta
	var started, ended, last string
	if err := row.Scan(&m.ID, &m.Workdir, &m.ConfigDir, &m.Model, &m.Status,
		&m.ParentID, &m.Agent, &m.CronJob, &started, &ended, &last, &m.LastPromptTokens,
		&m.TotalPromptTokens, &m.TotalCompletionTokens); err != nil {
		return Meta{}, err
	}
	m.StartedAt, m.EndedAt, m.LastAt = decTime(started), decTime(ended), decTime(last)
	return m, nil
}

// querySessions runs a metaColumns query for the list queries that differ
// only in their WHERE/ORDER BY/LIMIT.
func (s *Store) querySessions(query string, args ...any) ([]Meta, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("runs: list: %w", err)
	}
	defer rows.Close()
	var out []Meta
	for rows.Next() {
		m, err := scanMeta(rows)
		if err != nil {
			return nil, fmt.Errorf("runs: list: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ListSessions returns metas newest-first (by ID, which sorts chronologically).
func (s *Store) ListSessions(limit int) ([]Meta, error) {
	q := `SELECT ` + metaColumns + ` FROM sessions ORDER BY id DESC`
	if limit > 0 {
		return s.querySessions(q+` LIMIT ?`, limit)
	}
	return s.querySessions(q)
}

// SessionMeta returns one session's metadata.
func (s *Store) SessionMeta(id string) (Meta, error) {
	row := s.db.QueryRow(`SELECT `+metaColumns+` FROM sessions WHERE id = ?`, id)
	m, err := scanMeta(row)
	if err != nil {
		return Meta{}, fmt.Errorf("runs: session %s: %w", id, err)
	}
	return m, nil
}

// ReminderLine is one persisted system reminder for faithful replay.
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

// TruncateReminders clears the reminders of a session whose history was
// replaced.
func (s *Store) TruncateReminders(id string) error {
	if _, err := s.db.Exec(`DELETE FROM reminders WHERE session_id=?`, id); err != nil {
		return fmt.Errorf("runs: truncate reminders: %w", err)
	}
	return nil
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
			`DELETE FROM turn_prompts WHERE session_id=?`,
			`DELETE FROM threads WHERE session_id=?`,
			`DELETE FROM sessions WHERE id=?`,
		} {
			if _, err := tx.Exec(q, id); err != nil {
				return fmt.Errorf("runs: delete session %s: %w", id, err)
			}
		}
	}
	// Prompt bodies are shared between sessions, so they are collected once
	// the last reference is gone rather than deleted per session.
	if _, err := tx.Exec(
		`DELETE FROM prompts WHERE hash NOT IN (SELECT hash FROM turn_prompts)`); err != nil {
		return fmt.Errorf("runs: collect prompts: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("runs: delete sessions: %w", err)
	}
	for _, id := range ids {
		_ = os.RemoveAll(s.runDir(id)) // best-effort; job logs are disposable
	}
	return nil
}

// JobLogPath is runs/<id>/jobs/<jobID>.log, with the dir created so the caller
// can open it directly. Job logs stay plain files, not rows, so completion
// mail can point at them by path. "" when the dir cannot be created.
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

// runDir resolves a session's on-disk directory — job logs only, the
// conversation is in the database. IDs come from user-controlled surfaces, so
// anything that is not a plain path component is rejected.
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
