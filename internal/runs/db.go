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
// inspection by external tools. The schema is not an API: it changes without
// ceremony (see schemaVersion), so nothing in shell3 should read it this way.
func (s *Store) DBPath() string { return filepath.Join(s.root, DBFile) }

// schemaVersion is stamped into the database via `PRAGMA user_version` once
// the schema in this file has been applied. Bump it whenever schema.go's
// table shapes change in a way older code wrote data incompatible with (a
// new NOT NULL DEFAULT column is fine either way, but this repo bumps on any
// shape change rather than trying to judge compatibility case by case).
//
//  1. the shape `backup/telegram-0.3.2` shipped (sessions/messages/reminders/
//     threads(surface,msg_id,session_id)/messages_fts) — never stamped a
//     version itself, so a v1 database reads back as user_version 0 with
//     tables already present.
//  2. adds threads.title/preview/created_at/updated_at/deleted for webui's
//     richer thread metadata (see internal/webui/threads.go).
const schemaVersion = 2

// openDB opens path, applying the schema fresh or recreating the file
// outright when its stamped version doesn't match schemaVersion. Per the
// project's no-migration-ceremony rule (shell3 data is disposable), a
// mismatched database is never patched in place — it is deleted and rebuilt
// empty, loudly, rather than silently limping along on a schema the running
// binary doesn't fully understand (the failure mode this guards against:
// CREATE TABLE IF NOT EXISTS is a no-op against an existing table, so an
// older database's threads table would otherwise keep missing the new
// columns forever, and writes to them would silently degrade to
// memory-only).
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

	// A non-fresh database whose version doesn't match: recreate rather than
	// limp along on a shape this binary doesn't fully write.
	fmt.Fprintf(os.Stderr,
		"runs store: schema v%d != v%d — recreated %s (shell3 data is disposable by design)\n",
		version, schemaVersion, path)
	if err := db.Close(); err != nil {
		return nil, err
	}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := os.Remove(path + suffix); err != nil && !os.IsNotExist(err) {
			return nil, fmt.Errorf("runs: recreate db: remove %s%s: %w", path, suffix, err)
		}
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

// openRaw opens the database file with the store's connection pragmas, doing
// no schema work of its own.
func openRaw(path string) (*sql.DB, error) {
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
	title      TEXT NOT NULL DEFAULT '',
	preview    TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL DEFAULT '',
	updated_at TEXT NOT NULL DEFAULT '',
	deleted    INTEGER NOT NULL DEFAULT 0,
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
