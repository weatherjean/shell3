package runs

import (
	"database/sql"
	"net/url"
	"path/filepath"
	"testing"
)

func TestOpen_FreshDBIsStampedAndWorks(t *testing.T) {
	root := t.TempDir()
	st, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if err := st.SetCurrentSession("web", "s1"); err != nil {
		t.Fatalf("SetCurrentSession: %v", err)
	}
	if got, ok := st.CurrentSession("web"); !ok || got != "s1" {
		t.Fatalf("CurrentSession = %q, %v; want s1, true", got, ok)
	}

	v, err := userVersion(st.db)
	if err != nil {
		t.Fatal(err)
	}
	if v != schemaVersion {
		t.Errorf("user_version = %d, want %d", v, schemaVersion)
	}
}

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

	if _, ok := st.CurrentSession("web"); ok {
		t.Error("a recreated store should not carry over the old database's rows")
	}

	if err := st.SetCurrentSession("web", "s1"); err != nil {
		t.Fatalf("SetCurrentSession on recreated db: %v", err)
	}
	if got, ok := st.CurrentSession("web"); !ok || got != "s1" {
		t.Fatalf("CurrentSession on recreated db = %q, %v; want s1, true", got, ok)
	}
}

func TestCurrentSession_PerSurface(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if err := st.SetCurrentSession("telegram", "sess-1"); err != nil {
		t.Fatalf("SetCurrentSession: %v", err)
	}
	if err := st.SetCurrentSession("other", "sess-2"); err != nil {
		t.Fatalf("SetCurrentSession: %v", err)
	}
	if err := st.SetCurrentSession("telegram", "sess-3"); err != nil {
		t.Fatalf("SetCurrentSession: %v", err)
	}
	if got, ok := st.CurrentSession("telegram"); !ok || got != "sess-3" {
		t.Errorf("CurrentSession(telegram) = %q, %v; want sess-3, true", got, ok)
	}
	if got, ok := st.CurrentSession("other"); !ok || got != "sess-2" {
		t.Errorf("CurrentSession(serve) = %q, %v; want sess-2, true", got, ok)
	}
	if _, ok := st.CurrentSession("nope"); ok {
		t.Error("CurrentSession for an unrecorded surface should be absent")
	}
}

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
}
