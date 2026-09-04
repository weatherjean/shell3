// Package runs is the session store: one SQLite database per project at
// .shell3_project/shell3.db, holding sessions, messages, reminders, the
// front-end thread indexes, running command markers, and a full-text index.
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

// schemaVersion changes with every table-shape change. Mismatched databases
// are discarded and recreated rather than migrated or read with another shape.
const schemaVersion = 13

// openDB opens path, applying the one current schema. A mismatched database is
// deleted and recreated; shell3 has no schema migrations or compatibility
// readers. Corrupt and unreadable databases still return an error because a
// failure to inspect a file is not proof that it is merely stale.
func openDB(path string) (*sql.DB, error) {
	db, err := openRaw(path)
	if err != nil {
		return nil, err
	}

	fresh, err := isFreshDB(db)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	if fresh {
		if _, err := db.Exec(schema); err != nil {
			_ = db.Close()
			return nil, err
		}
		if err := setUserVersion(db, schemaVersion); err != nil {
			_ = db.Close()
			return nil, err
		}
		return db, nil
	}

	version, err := userVersion(db)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	if version == schemaVersion {
		// Idempotent: harmless to re-run against an already-current database.
		if _, err := db.Exec(schema); err != nil {
			_ = db.Close()
			return nil, err
		}
		return db, nil
	}

	fmt.Fprintf(os.Stderr, "runs store: schema v%d != v%d — discarding and resetting %s\n",
		version, schemaVersion, path)
	if err := db.Close(); err != nil {
		return nil, err
	}
	if err := removeDatabaseFiles(path); err != nil {
		return nil, err
	}

	db, err = openRaw(path)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := setUserVersion(db, schemaVersion); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// removeDatabaseFiles removes only the configured SQLite file and its two
// conventional sidecars. It never scans the directory or touches siblings.
func removeDatabaseFiles(path string) error {
	for _, candidate := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Remove(candidate); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("runs: reset db: remove %s: %w", candidate, err)
		}
	}
	return nil
}

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

// isFreshDB reports whether the database file has no tables of its own yet —
// a brand-new file, as opposed to one written by an older (or newer) version
// of this package. A fresh file always reads user_version 0, but so does a
// v1 database that never stamped one, so the two are told apart by whether
// sqlite_master has anything in it.
func isFreshDB(db *sql.DB) (bool, error) {
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table'`).Scan(&n); err != nil {
		return false, fmt.Errorf("runs: check schema: %w", err)
	}
	return n == 0, nil
}

func userVersion(db *sql.DB) (int, error) {
	var v int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		return 0, fmt.Errorf("runs: read schema version: %w", err)
	}
	return v, nil
}

func setUserVersion(db *sql.DB, v int) error {
	if _, err := db.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, v)); err != nil {
		return fmt.Errorf("runs: stamp schema version: %w", err)
	}
	return nil
}

const schema = `
CREATE TABLE IF NOT EXISTS sessions (
	id                 TEXT PRIMARY KEY,
	workdir            TEXT NOT NULL DEFAULT '',
	config_dir         TEXT NOT NULL DEFAULT '',
	model              TEXT NOT NULL DEFAULT '',
	status             TEXT NOT NULL DEFAULT 'live',
	parent_id          TEXT NOT NULL DEFAULT '',
	agent              TEXT NOT NULL DEFAULT '',
	cron_job           TEXT NOT NULL DEFAULT '',
	started_at         TEXT NOT NULL,
	ended_at           TEXT NOT NULL DEFAULT '',
	last_at            TEXT NOT NULL,
	last_prompt_tokens INTEGER NOT NULL DEFAULT 0,
	total_prompt_tokens     INTEGER NOT NULL DEFAULT 0,
	total_completion_tokens INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS messages (
	session_id TEXT    NOT NULL,
	seq        INTEGER NOT NULL,
	json       TEXT    NOT NULL,
	ts         TEXT    NOT NULL DEFAULT '',
	PRIMARY KEY (session_id, seq)
);
CREATE TABLE IF NOT EXISTS reminders (
	session_id TEXT    NOT NULL,
	seq        INTEGER NOT NULL,
	text       TEXT    NOT NULL
);
CREATE TABLE IF NOT EXISTS threads (
	surface    TEXT NOT NULL PRIMARY KEY,
	session_id TEXT NOT NULL
);
CREATE VIRTUAL TABLE IF NOT EXISTS messages_fts USING fts5(
	text, session_id UNINDEXED, seq UNINDEXED, role UNINDEXED
);
CREATE TABLE IF NOT EXISTS prompts (
	hash TEXT PRIMARY KEY,
	text TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS turn_prompts (
	session_id TEXT    NOT NULL,
	seq        INTEGER NOT NULL,
	hash       TEXT    NOT NULL,
	ts         TEXT    NOT NULL,
	PRIMARY KEY (session_id, seq)
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
