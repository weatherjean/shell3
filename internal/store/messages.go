package store

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/weatherjean/shell3/internal/llm"
)

// AppendMessage persists one conversation message at the given seq for a
// session. The full message is replayable: tool calls are JSON-encoded and
// tool results (RoleTool) are stored verbatim, unlike the lossy history FTS
// table. Idempotent on (session_id, seq) via INSERT OR REPLACE so a compaction
// rewrite can overwrite a seq in place.
//
// Note: llm.Message.ContentParts and llm.Message.ReasoningContent are not
// persisted. ContentParts is used for multimodal vision messages; ReasoningContent
// holds chain-of-thought from providers like Moonshot/DeepSeek. Both are
// omitempty and not needed for standard replay; they can be added later if
// required.
func (s *Store) AppendMessage(sessionID int64, seq int, m llm.Message) error {
	var toolCalls string
	if len(m.ToolCalls) > 0 {
		b, err := json.Marshal(m.ToolCalls)
		if err != nil {
			return fmt.Errorf("store: marshal tool calls: %w", err)
		}
		toolCalls = string(b)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.Exec(
		`INSERT OR REPLACE INTO messages
		 (session_id, seq, role, content, tool_calls_json, tool_call_id, name, created_at)
		 VALUES(?,?,?,?,?,?,?,?)`,
		sessionID, seq, string(m.Role), m.Content, toolCalls, m.ToolCallID, m.Name, now,
	)
	if err != nil {
		return fmt.Errorf("store: append message: %w", err)
	}
	return nil
}

// DeleteMessagesFrom removes all messages at seq >= from for a session. Used by
// compaction mirroring to collapse a range before re-inserting the summary.
func (s *Store) DeleteMessagesFrom(sessionID int64, from int) error {
	if _, err := s.db.Exec(
		`DELETE FROM messages WHERE session_id = ? AND seq >= ?`, sessionID, from,
	); err != nil {
		return fmt.Errorf("store: delete messages: %w", err)
	}
	return nil
}

// LoadSessionMessages reconstructs the full ordered message slice for a session,
// suitable for seeding a resumed chat.Session.
func (s *Store) LoadSessionMessages(sessionID int64) ([]llm.Message, error) {
	rows, err := s.db.Query(
		`SELECT role, content, tool_calls_json, tool_call_id, name
		 FROM messages WHERE session_id = ? ORDER BY seq ASC`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("store: load messages %d: %w", sessionID, err)
	}
	defer rows.Close()
	var out []llm.Message
	for rows.Next() {
		var m llm.Message
		var role, toolCalls string
		if err := rows.Scan(&role, &m.Content, &toolCalls, &m.ToolCallID, &m.Name); err != nil {
			return nil, fmt.Errorf("store: load messages: scan: %w", err)
		}
		m.Role = llm.Role(role)
		if toolCalls != "" {
			if err := json.Unmarshal([]byte(toolCalls), &m.ToolCalls); err != nil {
				return nil, fmt.Errorf("store: load messages: unmarshal tool calls: %w", err)
			}
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
