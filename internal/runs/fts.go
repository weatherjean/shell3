package runs

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/weatherjean/shell3/internal/llm"
)

// searchableText extracts the text worth full-text indexing from a message:
// user and assistant prose (plain content and text parts). Tool output and
// system prompts are skipped — they dwarf the conversation and bury search
// results in noise.
func searchableText(m llm.Message) string {
	if m.Role != llm.RoleUser && m.Role != llm.RoleAssistant {
		return ""
	}
	var b strings.Builder
	if m.Content != "" {
		b.WriteString(m.Content)
	}
	for _, p := range m.ContentParts {
		if p.Type == llm.ContentPartTypeText && p.Text != "" {
			if b.Len() > 0 {
				b.WriteByte('\n')
			}
			b.WriteString(p.Text)
		}
	}
	return strings.TrimSpace(b.String())
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
}

// Search runs an FTS5 query over the indexed conversation text (user and
// assistant messages), best matches first. query uses FTS5 syntax: bare words
// AND together, quotes make phrases.
func (s *Store) Search(query string, limit int) ([]SearchHit, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Query(`SELECT session_id, seq, role,
		snippet(messages_fts, 0, '[', ']', '…', 16)
		FROM messages_fts WHERE messages_fts MATCH ?
		ORDER BY rank LIMIT ?`, query, limit)
	if err != nil {
		return nil, fmt.Errorf("runs: search: %w", err)
	}
	defer rows.Close()
	var out []SearchHit
	for rows.Next() {
		var h SearchHit
		if err := rows.Scan(&h.SessionID, &h.Seq, &h.Role, &h.Snippet); err != nil {
			return nil, fmt.Errorf("runs: search: %w", err)
		}
		out = append(out, h)
	}
	return out, rows.Err()
}
