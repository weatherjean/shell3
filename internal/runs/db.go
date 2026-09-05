// Package runs is the session store: one SQLite database per project at
// .shell3_project/shell3.db, holding sessions, messages, reminders, front-end
// conversation markers, running command markers, and schedule invocations.
// Job logs stay plain files beside it, and inbox notices may point to them.
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

// Open ensures root exists, opens root/shell3.db, and applies the schema.
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

// openDB opens path and applies the current schema.
func openDB(path string) (*sql.DB, error) {
	db, err := openRaw(path)
	if err != nil {
		return nil, err
	}
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		_ = db.Close()
		return nil, err
	}
	if version > schemaVersion {
		_ = db.Close()
		return nil, fmt.Errorf("runs: database schema version %d is newer than supported version %d", version, schemaVersion)
	}
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

const schemaVersion = 1

// openRaw opens the database file with the store's connection pragmas, doing
// no schema work of its own.
func openRaw(path string) (*sql.DB, error) {
	// WAL for multi-process readers (one-shot shell3 can run alongside an
	// telegram over one store), busy_timeout so a brief writer overlap
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
	return db, nil
}

const schema = `
CREATE TABLE IF NOT EXISTS sessions (
	id                 TEXT PRIMARY KEY,
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
CREATE TABLE IF NOT EXISTS current_sessions (
	surface    TEXT NOT NULL PRIMARY KEY,
	session_id TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS background_jobs (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	pid INTEGER NOT NULL,
	job_id TEXT NOT NULL,
	title TEXT NOT NULL,
	owner_id TEXT NOT NULL DEFAULT '',
	log_path TEXT NOT NULL DEFAULT '',
	started_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS schedule_runs (
	id            TEXT PRIMARY KEY,
	schedule_name TEXT NOT NULL,
	task          TEXT NOT NULL,
	trigger       TEXT NOT NULL,
	run_dir       TEXT NOT NULL,
	output_path   TEXT NOT NULL,
	scheduled_at  TEXT NOT NULL,
	started_at    TEXT NOT NULL,
	finished_at   TEXT NOT NULL DEFAULT '',
	status        TEXT NOT NULL CHECK (status IN ('running','done','failed')),
	error         TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS schedule_runs_name_started
	ON schedule_runs(schedule_name, started_at DESC);
PRAGMA user_version = 1;
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
