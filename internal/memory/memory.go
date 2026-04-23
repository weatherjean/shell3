// Package memory provides a SQLite FTS5-backed key-value memory store.
package memory

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"
)

// Entry is one memory record.
type Entry struct {
	Key       string
	Value     string
	UpdatedAt time.Time
}

// DB wraps a SQLite FTS5 memory store.
type DB struct {
	sql *sql.DB
}

// Open opens or creates the SQLite memory database at path.
func Open(path string) (*DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("memory: open %s: %w", path, err)
	}
	_, err = db.Exec(`
		CREATE VIRTUAL TABLE IF NOT EXISTS memories USING fts5(
			key,
			value,
			updated_at UNINDEXED
		)
	`)
	if err != nil {
		return nil, fmt.Errorf("memory: create table: %w", err)
	}
	return &DB{sql: db}, nil
}

// Store upserts key with value into the memory store.
func (d *DB) Store(key, value string) error {
	now := time.Now().UTC().Format(time.RFC3339)
	// FTS5 has no native upsert — delete then insert
	if _, err := d.sql.Exec(`DELETE FROM memories WHERE key = ?`, key); err != nil {
		return fmt.Errorf("memory: store delete: %w", err)
	}
	if _, err := d.sql.Exec(`INSERT INTO memories(key, value, updated_at) VALUES(?, ?, ?)`, key, value, now); err != nil {
		return fmt.Errorf("memory: store insert: %w", err)
	}
	return nil
}

// Search runs an FTS5 full-text search and returns up to 5 results by BM25 rank.
func (d *DB) Search(query string) ([]Entry, error) {
	rows, err := d.sql.Query(`
		SELECT key, value, updated_at
		FROM memories
		WHERE memories MATCH ?
		ORDER BY rank
		LIMIT 5
	`, query)
	if err != nil {
		return nil, fmt.Errorf("memory: search: %w", err)
	}
	defer rows.Close()

	var results []Entry
	for rows.Next() {
		var e Entry
		var updatedAt string
		if err := rows.Scan(&e.Key, &e.Value, &updatedAt); err != nil {
			return nil, fmt.Errorf("memory: scan: %w", err)
		}
		e.UpdatedAt, _ = time.Parse(time.RFC3339, updatedAt)
		results = append(results, e)
	}
	return results, rows.Err()
}

// Close closes the underlying database connection.
func (d *DB) Close() error { return d.sql.Close() }
