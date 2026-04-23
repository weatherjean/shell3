// Package store provides a SQLite-backed store for memories, history, and sessions.
package store

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// Store wraps a SQLite database with tables for sessions, history, and memories.
type Store struct{ db *sql.DB }

// Open opens or creates the SQLite store at path and runs schema migrations.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func migrate(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS sessions (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			started_at TEXT NOT NULL,
			ended_at   TEXT,
			summary    TEXT
		)`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS history USING fts5(
			content,
			session_id UNINDEXED,
			role       UNINDEXED,
			created_at UNINDEXED
		)`,
		`CREATE VIRTUAL TABLE IF NOT EXISTS memories USING fts5(
			key,
			value,
			updated_at UNINDEXED
		)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("store: migrate: %w", err)
		}
	}
	return nil
}

// Close closes the underlying database connection.
func (s *Store) Close() error { return s.db.Close() }

// MemoryEntry is one memory record.
type MemoryEntry struct {
	Key       string
	Value     string
	UpdatedAt time.Time
}

// MemoryStore upserts key with value into the memory store.
func (s *Store) MemoryStore(key, value string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := s.db.Exec(`DELETE FROM memories WHERE key = ?`, key); err != nil {
		return fmt.Errorf("store: memory delete: %w", err)
	}
	if _, err := s.db.Exec(`INSERT INTO memories(key, value, updated_at) VALUES(?, ?, ?)`, key, value, now); err != nil {
		return fmt.Errorf("store: memory insert: %w", err)
	}
	return nil
}

// MemorySearch runs an FTS5 full-text search and returns up to limit results by BM25 rank.
func (s *Store) MemorySearch(query string, limit int) ([]MemoryEntry, error) {
	rows, err := s.db.Query(`
		SELECT key, value, updated_at
		FROM memories
		WHERE memories MATCH ?
		ORDER BY rank
		LIMIT ?
	`, query, limit)
	if err != nil {
		return nil, fmt.Errorf("store: memory search: %w", err)
	}
	defer rows.Close()

	var results []MemoryEntry
	for rows.Next() {
		var e MemoryEntry
		var updatedAt string
		if err := rows.Scan(&e.Key, &e.Value, &updatedAt); err != nil {
			return nil, fmt.Errorf("store: memory scan: %w", err)
		}
		e.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		results = append(results, e)
	}
	return results, rows.Err()
}

// MemoryDelete removes the entry with the given key from the memory store.
func (s *Store) MemoryDelete(key string) error {
	if _, err := s.db.Exec(`DELETE FROM memories WHERE key = ?`, key); err != nil {
		return fmt.Errorf("store: memory delete %q: %w", key, err)
	}
	return nil
}

// StartSession inserts a new session row and returns its id.
func (s *Store) StartSession() (int64, error) {
	now := time.Now().UTC().Format(time.RFC3339)
	res, err := s.db.Exec(`INSERT INTO sessions(started_at) VALUES(?)`, now)
	if err != nil {
		return 0, fmt.Errorf("store: start session: %w", err)
	}
	return res.LastInsertId()
}

// EndSession sets ended_at for the given session.
func (s *Store) EndSession(id int64) error {
	now := time.Now().UTC().Format(time.RFC3339)
	if _, err := s.db.Exec(`UPDATE sessions SET ended_at = ? WHERE id = ?`, now, id); err != nil {
		return fmt.Errorf("store: end session %d: %w", id, err)
	}
	return nil
}

// AppendHistory stores one conversation turn in the history FTS5 table.
func (s *Store) AppendHistory(sessionID int64, role, content string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	_, err := s.db.Exec(
		`INSERT INTO history(content, session_id, role, created_at) VALUES(?, ?, ?, ?)`,
		content, sessionID, role, now,
	)
	if err != nil {
		return fmt.Errorf("store: append history: %w", err)
	}
	return nil
}

// HistoryResult is one row from a history search.
type HistoryResult struct {
	SessionID        int64
	Role             string
	Content          string
	CreatedAt        time.Time
	SessionStartedAt time.Time
}

// SearchHistory runs an FTS5 search on history content and returns up to limit results.
func (s *Store) SearchHistory(query string, limit int) ([]HistoryResult, error) {
	rows, err := s.db.Query(`
		SELECT h.session_id, h.role, h.content, h.created_at, s.started_at
		FROM (
			SELECT session_id, role, content, created_at
			FROM history
			WHERE history MATCH ?
			ORDER BY rank
			LIMIT ?
		) h
		JOIN sessions s ON CAST(h.session_id AS INTEGER) = s.id
	`, query, limit)
	if err != nil {
		return nil, fmt.Errorf("store: search history: %w", err)
	}
	defer rows.Close()

	var results []HistoryResult
	for rows.Next() {
		var r HistoryResult
		var createdAt, sessionStartedAt string
		if err := rows.Scan(&r.SessionID, &r.Role, &r.Content, &createdAt, &sessionStartedAt); err != nil {
			return nil, fmt.Errorf("store: history scan: %w", err)
		}
		r.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
		r.SessionStartedAt, _ = time.Parse(time.RFC3339, sessionStartedAt)
		results = append(results, r)
	}
	return results, rows.Err()
}
