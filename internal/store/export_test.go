package store

import "database/sql"

// DB exposes the underlying *sql.DB for tests.
func (s *Store) DB() *sql.DB { return s.db }

// MaxOpenConns exposes the configured pool limit for tests.
func (s *Store) MaxOpenConns() int { return s.db.Stats().MaxOpenConnections }

// JournalMode reports the effective SQLite journal mode (e.g. "wal", "memory",
// "delete") for tests asserting the WAL flip is gated to file-backed DBs.
func (s *Store) JournalMode() (string, error) {
	var mode string
	err := s.db.QueryRow("PRAGMA journal_mode").Scan(&mode)
	return mode, err
}
