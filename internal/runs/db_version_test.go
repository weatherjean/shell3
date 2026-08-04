package runs

import (
	"database/sql"
	"net/url"
	"path/filepath"
	"testing"
)

// TestOpen_FreshDBIsStampedAndWorks: a brand-new store gets user_version
// stamped to schemaVersion, and the store works normally.
func TestOpen_FreshDBIsStampedAndWorks(t *testing.T) {
	root := t.TempDir()
	st, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if err := st.ThreadUpsertMeta("web", ThreadMeta{ID: "t1", SessionID: "s1"}); err != nil {
		t.Fatalf("ThreadUpsertMeta: %v", err)
	}
	metas, err := st.ThreadListMeta("web")
	if err != nil || len(metas) != 1 {
		t.Fatalf("ThreadListMeta: %v, %+v", err, metas)
	}

	v, err := userVersion(st.db)
	if err != nil {
		t.Fatal(err)
	}
	if v != schemaVersion {
		t.Errorf("user_version = %d, want %d", v, schemaVersion)
	}
}

// TestOpen_RecreatesAMismatchedDB builds a v1-shaped database — the shape
// backup/telegram-0.3.2 shipped: sessions/messages/reminders/messages_fts and
// a threads table with only surface/msg_id/session_id, and no user_version
// ever stamped (so it reads back as 0, but WITH tables present — the thing
// that distinguishes it from a truly fresh, empty file). Open must recreate
// the file from scratch rather than limp along missing the new columns.
func TestOpen_RecreatesAMismatchedDB(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, DBFile)
	buildV1DB(t, path)

	st, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	v, err := userVersion(st.db)
	if err != nil {
		t.Fatal(err)
	}
	if v != schemaVersion {
		t.Errorf("user_version after recreate = %d, want %d", v, schemaVersion)
	}

	// The v1 database's row is gone — the file was deleted and rebuilt, not
	// migrated in place.
	if _, ok := st.ThreadLookup("web", "old-thread"); ok {
		t.Error("a recreated store should not carry over the old database's rows")
	}

	// The new columns work.
	if err := st.ThreadUpsertMeta("web", ThreadMeta{ID: "t1", SessionID: "s1", Title: "hi"}); err != nil {
		t.Fatalf("ThreadUpsertMeta on recreated db: %v", err)
	}
	metas, err := st.ThreadListMeta("web")
	if err != nil || len(metas) != 1 || metas[0].Title != "hi" {
		t.Fatalf("ThreadListMeta on recreated db: %v, %+v", err, metas)
	}
}

// TestOpen_TelegramSurfaceStillWorksOnFreshDB: ThreadRecord/ThreadLookup —
// the interface telegram's ThreadIndex uses — are unaffected by the new
// columns on a fresh v2 database.
func TestOpen_TelegramSurfaceStillWorksOnFreshDB(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if err := st.ThreadRecord("telegram", "msg-1", "sess-1"); err != nil {
		t.Fatalf("ThreadRecord: %v", err)
	}
	got, ok := st.ThreadLookup("telegram", "msg-1")
	if !ok || got != "sess-1" {
		t.Errorf("ThreadLookup(telegram, msg-1) = %q, %v; want sess-1, true", got, ok)
	}
}

// buildV1DB writes a database at path in the shape backup/telegram-0.3.2
// shipped — no title/preview/created_at/updated_at/deleted columns on
// threads, and no user_version ever stamped — with one row in threads so a
// recreate is observable.
func buildV1DB(t *testing.T, path string) {
	t.Helper()
	dsn := "file:" + url.PathEscape(path) + "?_pragma=journal_mode(WAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	const v1Schema = `
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
	if _, err := db.Exec(v1Schema); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO threads (surface, msg_id, session_id) VALUES ('web','old-thread','sess-old')`); err != nil {
		t.Fatal(err)
	}
	// v1 never stamped user_version; leave it at SQLite's default (0).
}
