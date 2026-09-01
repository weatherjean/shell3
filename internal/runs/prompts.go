package runs

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"
)

// System prompts are stored by content hash and referenced per turn. They are
// intentionally excluded from conversation search.

// PromptRecord is one turn's system prompt, as stored.
type PromptRecord struct {
	Seq  int    // the message seq this prompt was used from
	Hash string // content hash (sha256, hex, first 16 bytes)
	Text string
	TS   time.Time
}

// promptHash is the content address of a prompt body.
func promptHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:16])
}

// SavePrompt records the system prompt a turn ran with, at the message seq the
// turn started from. Idempotent: re-recording the same (session, seq) replaces
// it, and an identical body is stored once however many turns use it.
//
// A turn whose prompt is byte-identical to the previous turn's in the same
// session is NOT re-recorded — the reference would carry no information, and
// the common case (nothing changed) should cost nothing but the lookup.
func (s *Store) SavePrompt(sessionID string, seq int, text string, at time.Time) error {
	if s == nil || sessionID == "" || text == "" {
		return nil
	}
	hash := promptHash(text)

	var prev string
	err := s.db.QueryRow(
		`SELECT hash FROM turn_prompts WHERE session_id=? ORDER BY seq DESC LIMIT 1`, sessionID,
	).Scan(&prev)
	if err == nil && prev == hash {
		return nil // unchanged since the last recorded turn
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("runs: save prompt: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`INSERT OR IGNORE INTO prompts (hash, text) VALUES (?, ?)`, hash, text); err != nil {
		return fmt.Errorf("runs: save prompt: %w", err)
	}
	if _, err := tx.Exec(
		`INSERT OR REPLACE INTO turn_prompts (session_id, seq, hash, ts) VALUES (?, ?, ?, ?)`,
		sessionID, seq, hash, encTime(at),
	); err != nil {
		return fmt.Errorf("runs: save prompt: %w", err)
	}
	return tx.Commit()
}

// PromptsForSession returns every recorded prompt version for a session, in
// the order they took effect. Each entry is a CHANGE: the prompt held from its
// seq until the next entry's.
func (s *Store) PromptsForSession(sessionID string) []PromptRecord {
	var out []PromptRecord
	if s == nil {
		return out
	}
	rows, err := s.db.Query(
		`SELECT tp.seq, tp.hash, tp.ts, p.text
		   FROM turn_prompts tp JOIN prompts p ON p.hash = tp.hash
		  WHERE tp.session_id = ? ORDER BY tp.seq`, sessionID)
	if err != nil {
		return out
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var r PromptRecord
		var ts string
		if err := rows.Scan(&r.Seq, &r.Hash, &ts, &r.Text); err != nil {
			continue
		}
		r.TS = decTime(ts)
		out = append(out, r)
	}
	return out
}
