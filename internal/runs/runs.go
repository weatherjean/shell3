package runs

import (
	"database/sql"
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
	// Agent is the name of the agent that ran this session. It is what makes
	// "show me what bookmarks did" answerable — auditing an employee needs its
	// runs findable by name, not just by id.
	Agent string `json:"agent,omitempty"`
	// CronJob is the name of the cron job that started this session ("" for
	// a front-end or task-tool session). It is what makes "what did this job
	// do" answerable without guessing from session duration.
	CronJob   string    `json:"cron_job,omitempty"`
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
	// TotalPromptTokens and TotalCompletionTokens are a cumulative ledger of
	// every turn's provider-reported usage for this session — distinct from
	// LastPromptTokens, which is overwritten each turn and answers "how full is
	// the context now". These only grow (see Store.AddUsage) and answer "what
	// did this session cost", the question Store.CronRollup totals per cron job.
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
	// One clock reading for both the row and the session's recency, so a
	// message can never look newer than the session that holds it.
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

// AddUsage accumulates one turn's provider-reported token usage onto the
// session's cumulative ledger (see Meta.TotalPromptTokens). LastPromptTokens
// answers "how full is the context now"; this answers "what did this session
// cost", a different question an operator running unattended cron work needs
// answered — see Store.CronRollup. Unknown session ids error rather than
// silently no-op, since a cost that never lands anywhere is worse than a loud
// failure naming the id at fault.
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

// metaColumns is the column list shared by every query that scans a full
// Meta row (ListSessions, SessionMeta) — kept in one
// place so the SELECT list and the Scan call below can never drift apart.
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

// querySessions runs a metaColumns query and scans every row, for the
// several list queries that differ only
// in their WHERE/ORDER BY/LIMIT clause.
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
			`DELETE FROM turn_prompts WHERE session_id=?`,
			`DELETE FROM threads WHERE session_id=?`,
			`DELETE FROM sessions WHERE id=?`,
		} {
			if _, err := tx.Exec(q, id); err != nil {
				return fmt.Errorf("runs: delete session %s: %w", id, err)
			}
		}
	}
	// Prompt bodies are shared between sessions (an unchanged prompt is one
	// row however many conversations used it), so they are collected once the
	// last reference is gone rather than deleted per session.
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

// JobLogPath returns the on-disk log path for a background job owned by
// session id — runs/<id>/jobs/<jobID>.log — creating the dir so the caller
// can open the file directly. Job logs stay plain files (not database rows)
// so the completion mail can point at them by path. Returns "" when the dir
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
