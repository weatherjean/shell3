package runs

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/weatherjean/shell3/internal/llm"
)

// searchableText extracts the text worth full-text indexing from a message:
// user and assistant prose. Tool output and system prompts are skipped —
// they dwarf the conversation and bury search results in noise.
func searchableText(m llm.Message) string {
	if m.Role != llm.RoleUser && m.Role != llm.RoleAssistant {
		return ""
	}
	return strings.TrimSpace(m.Content)
}

// SeqMessage is one stored message with its position in the session.
type SeqMessage struct {
	Seq int
	Msg llm.Message
}

// MessagesRange reads the session's messages with seq in [from, to], in
// order — the read-around-a-hit half of the history tool.
func (s *Store) MessagesRange(id string, from, to int) ([]SeqMessage, error) {
	rows, err := s.db.Query(`SELECT seq, json FROM messages
		WHERE session_id=? AND seq BETWEEN ? AND ? ORDER BY seq`, id, from, to)
	if err != nil {
		return nil, fmt.Errorf("runs: messages range %s: %w", id, err)
	}
	defer rows.Close()
	var out []SeqMessage
	for rows.Next() {
		var sm SeqMessage
		var raw string
		if err := rows.Scan(&sm.Seq, &raw); err != nil {
			return nil, fmt.Errorf("runs: messages range %s: %w", id, err)
		}
		if err := json.Unmarshal([]byte(raw), &sm.Msg); err != nil {
			return nil, fmt.Errorf("runs: messages range %s: %w", id, err)
		}
		out = append(out, sm)
	}
	return out, rows.Err()
}

// SearchHit is one full-text match in the conversation history.
type SearchHit struct {
	SessionID string
	Seq       int
	Role      string
	Snippet   string
	Agent     string
	CronJob   string
	ParentID  string
	StartedAt time.Time
}

// SearchFilter narrows full-text matches by the session that owns them.
// Empty fields do not filter. Since is inclusive; Before is exclusive.
type SearchFilter struct {
	Agent    string
	CronJob  string
	ParentID string
	Since    time.Time
	Before   time.Time
}

// Search runs an FTS5 query over the indexed conversation text (user and
// assistant messages), best matches first. query uses FTS5 syntax: bare words
// AND together, quotes make phrases.
func (s *Store) Search(query string, limit int) ([]SearchHit, error) {
	return s.SearchFiltered(query, SearchFilter{}, limit)
}

// SearchFiltered runs Search with optional session metadata and time bounds.
// Filtering happens in SQLite before LIMIT, so selective queries do not lose
// matches to newer sessions outside the requested scope.
func (s *Store) SearchFiltered(query string, filter SearchFilter, limit int) ([]SearchHit, error) {
	if limit <= 0 {
		limit = 20
	}
	since, before := "", ""
	if !filter.Since.IsZero() {
		since = encTime(filter.Since)
	}
	if !filter.Before.IsZero() {
		before = encTime(filter.Before)
	}
	rows, err := s.db.Query(`SELECT f.session_id, f.seq, f.role,
		snippet(messages_fts, 0, '[', ']', '…', 16),
		s.agent, s.cron_job, s.parent_id, s.started_at
		FROM messages_fts AS f JOIN sessions AS s ON s.id=f.session_id
		WHERE messages_fts MATCH ?
		AND (?='' OR lower(s.agent)=lower(?))
		AND (?='' OR s.cron_job=?)
		AND (?='' OR s.parent_id=?)
		AND (?='' OR s.started_at>=?)
		AND (?='' OR s.started_at<?)
		ORDER BY rank LIMIT ?`,
		query,
		filter.Agent, filter.Agent,
		filter.CronJob, filter.CronJob,
		filter.ParentID, filter.ParentID,
		since, since,
		before, before,
		limit)
	if err != nil {
		return nil, fmt.Errorf("runs: search: %w", err)
	}
	defer rows.Close()
	var out []SearchHit
	for rows.Next() {
		var h SearchHit
		var started string
		if err := rows.Scan(&h.SessionID, &h.Seq, &h.Role, &h.Snippet,
			&h.Agent, &h.CronJob, &h.ParentID, &started); err != nil {
			return nil, fmt.Errorf("runs: search: %w", err)
		}
		h.StartedAt = decTime(started)
		out = append(out, h)
	}
	return out, rows.Err()
}
