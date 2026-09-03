// Package runs is the session store: one SQLite database per project at
// .shell3_project/shell3.db, holding sessions, messages, reminders, the
// front-end thread indexes and a full-text index. Job logs stay plain files
// beside it, so completion mail can point at them by path.
package runs

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
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
// are archived and recreated rather than migrated or read with an older shape.
const schemaVersion = 12

// openDB opens path, applying the schema fresh or recreating the file when
// its stamp does not match schemaVersion. A mismatched database is never
// patched in place; its database/WAL/SHM bundle is archived together.
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
	// user_version is a bare unchecksummed field, so a corrupted byte reads
	// back as an implausible version over intact tables. A version this
	// binary could not have written is an error, never grounds to delete.
	if version < 0 || version > schemaVersion {
		_ = db.Close()
		return nil, fmt.Errorf(
			"runs: %s has an unrecognised schema stamp (user_version=%d, this binary understands 0..%d) — "+
				"not recreating a database that might just be corrupt or from a newer binary; "+
				"move or delete it yourself if you want a fresh store", path, version, schemaVersion)
	}
	if version == schemaVersion {
		// Idempotent: harmless to re-run against an already-current database.
		if _, err := db.Exec(schema); err != nil {
			_ = db.Close()
			return nil, err
		}
		return db, nil
	}

	// Old but plausible: recreate rather than limp along on a shape this
	// binary does not fully write. Move the old files aside rather than
	// unlinking — a rename is atomic, so a failure partway through building
	// the new schema never leaves the operator with no store at all.
	//
	oldSuffix := fmt.Sprintf(".old-v%d-%d", version, time.Now().UnixNano())
	fmt.Fprintf(os.Stderr,
		"runs store: schema v%d != v%d — archiving and resetting %s\n",
		version, schemaVersion, path)
	if err := db.Close(); err != nil {
		return nil, err
	}
	// -wal/-shm first: a suffix rename failing partway leaves the main
	// database file, and its data, exactly where it was.
	asided, err := moveOldFilesAside(path, oldSuffix)
	if err != nil {
		return nil, recreateErr(asided, err)
	}

	db, err = openRaw(path)
	if err != nil {
		return nil, recreateErr(asided, err)
	}
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, recreateErr(asided, err)
	}
	if err := setUserVersion(db, schemaVersion); err != nil {
		_ = db.Close()
		return nil, recreateErr(asided, err)
	}
	// An old store is user history, not a temporary rollback file. Keep the
	// complete bundle after a successful rebuild and name every path so the
	// operator can archive, inspect with the matching old binary, or delete it.
	fmt.Fprintf(os.Stderr, "runs store: previous schema preserved at: %s\n", strings.Join(asided, ", "))
	return db, nil
}

// moveOldFilesAside renames path and its -wal/-shm siblings (whichever
// exist) to path+suffix/path-wal+suffix/path-shm+suffix, in that order —
// -wal/-shm first so a failure partway through never removes the main
// database file while a sibling still carries the old name. It touches
// nothing else in the directory: a sibling file that doesn't share path's
// base name (or the -wal/-shm suffix) is never even stat'd. Returns the
// list of files successfully moved aside, even when it later returns an
// error, so a caller can report (or clean up) exactly what survived.
func moveOldFilesAside(path, suffix string) ([]string, error) {
	var asided []string
	for _, s := range []string{"-wal", "-shm", ""} {
		src := path + s
		if _, statErr := os.Stat(src); statErr != nil {
			if os.IsNotExist(statErr) {
				continue
			}
			return asided, fmt.Errorf("runs: recreate db: stat %s: %w", src, statErr)
		}
		dst := src + suffix
		if err := os.Rename(src, dst); err != nil {
			return asided, fmt.Errorf("runs: recreate db: move %s aside: %w", src, err)
		}
		asided = append(asided, dst)
	}
	return asided, nil
}

// recreateErr wraps a failure that happened while rebuilding the store. When
// asided is non-empty, the old files are still sitting on disk under their
// aside names and the operator can recover by hand — so the error names
// every one of them and spells out the pairing hazard: the main db and its
// -wal/-shm siblings all carry the same ".old-..." trailing suffix
// (shell3.db.old-v1-123, shell3.db-wal.old-v1-123, shell3.db-shm.old-v1-123),
// which is not a name SQLite will pair on its own — renaming only the main
// file back leaves a stale/mismatched -wal or -shm sitting next to it, so
// recovery means renaming every aside file back (dropping its trailing
// ".old-..." suffix) together, not just one of them.
func recreateErr(asided []string, cause error) error {
	if len(asided) == 0 {
		return fmt.Errorf("runs: recreate db: %w", cause)
	}
	return fmt.Errorf(
		"runs: recreate db: %w — old data was moved aside to: %s "+
			"(to recover by hand, rename every one of those files back to its "+
			"original name — drop the trailing \".old-...\" suffix — together; "+
			"renaming only the main db file back leaves a stale/mismatched -wal "+
			"or -shm next to it, since SQLite pairs them by exact name, not by "+
			"a shared suffix)",
		cause, strings.Join(asided, ", "))
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
CREATE TABLE IF NOT EXISTS outbox (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	kind TEXT NOT NULL,
	json TEXT NOT NULL
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
