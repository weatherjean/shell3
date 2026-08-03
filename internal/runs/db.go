// Package runs is the session store: one SQLite database per project at
// .shell3_project/shell3.db holding sessions, messages, reminders, the
// front-end thread indexes, and a full-text index over the conversation.
// Background job logs stay plain files beside it (runs/<id>/jobs/) so the
// notifier's read tool can open them by path.
package runs

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver; keeps cross-compiled release builds cgo-free
)

// DBFile is the database filename under the store root (.shell3_project/).
const DBFile = "shell3.db"

// Store is rooted at a project's .shell3_project/ directory.
type Store struct {
	root string
	db   *sql.DB
}

// Open ensures root exists, opens (creating if needed) root/shell3.db, and
// applies the schema. root is the .shell3_project/ directory.
func Open(root string) (*Store, error) {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("runs: open %s: %w", root, err)
	}
	db, err := openDB(filepath.Join(root, DBFile))
	if err != nil {
		return nil, fmt.Errorf("runs: open db: %w", err)
	}
	return &Store{root: root, db: db}, nil
}

// Close releases the database handle. Safe on a nil store.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// DBPath returns the on-disk path of the store's database, for read-only
// access by external tools (the history skill's sqlite3 queries).
func (s *Store) DBPath() string { return filepath.Join(s.root, DBFile) }

func openDB(path string) (*sql.DB, error) {
	// WAL for multi-process readers (shell3 telegram and shell3 ask can run
	// concurrently over one store), busy_timeout so a brief writer overlap
	// waits instead of erroring.
	dsn := "file:" + url.PathEscape(path) +
		"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// One connection per handle: sqlite has a single writer anyway, and a
	// serialized pool means intra-process writes never contend.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

const schema = `
CREATE TABLE IF NOT EXISTS sessions (
	id                 TEXT PRIMARY KEY,
	workdir            TEXT NOT NULL DEFAULT '',
	config_dir         TEXT NOT NULL DEFAULT '',
	model              TEXT NOT NULL DEFAULT '',
	status             TEXT NOT NULL DEFAULT 'live',
	parent_id          TEXT NOT NULL DEFAULT '',
	started_at         TEXT NOT NULL,
	ended_at           TEXT NOT NULL DEFAULT '',
	last_at            TEXT NOT NULL,
	last_prompt_tokens INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS messages (
	session_id TEXT    NOT NULL,
	seq        INTEGER NOT NULL,
	json       TEXT    NOT NULL,
	PRIMARY KEY (session_id, seq)
);
CREATE TABLE IF NOT EXISTS reminders (
	session_id TEXT    NOT NULL,
	seq        INTEGER NOT NULL,
	text       TEXT    NOT NULL
);
CREATE TABLE IF NOT EXISTS threads (
	surface    TEXT NOT NULL,
	msg_id     TEXT NOT NULL,
	session_id TEXT NOT NULL,
	PRIMARY KEY (surface, msg_id)
);
CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
	text, session_id UNINDEXED, seq UNINDEXED, role UNINDEXED
);
`

// newID is a sortable wall-clock timestamp plus a random suffix, so ids order
// chronologically and never collide across concurrent processes.
func newID() string {
	var b [4]byte
	_, _ = rand.Read(b[:]) // on the astronomically unlikely error, fall back to timestamp-only
	return time.Now().UTC().Format("20060102T150405.000000000") + "-" + hex.EncodeToString(b[:])
}

// Timestamps are stored as RFC3339Nano UTC text; "" is the zero time.
func encTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func decTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t
}
