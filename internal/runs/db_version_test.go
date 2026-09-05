package runs

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestOpen_CurrentSchemaWorks(t *testing.T) {
	root := t.TempDir()
	st, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if err := st.SetCurrentSession("telegram:1", "s1"); err != nil {
		t.Fatalf("SetCurrentSession: %v", err)
	}
	if got, ok := st.CurrentSession("telegram:1"); !ok || got != "s1" {
		t.Fatalf("CurrentSession = %q, %v; want s1, true", got, ok)
	}

	var version int
	if err := st.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != schemaVersion {
		t.Errorf("user_version = %d, want %d", version, schemaVersion)
	}
}

func TestOpen_RejectsNewerSchema(t *testing.T) {
	root := t.TempDir()
	db, err := openRaw(filepath.Join(root, DBFile))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA user_version = 99`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(root); err == nil || !strings.Contains(err.Error(), "newer than supported") {
		t.Fatalf("Open newer schema error = %v", err)
	}
}

func TestOpen_ReappliesSchemaWithoutResettingState(t *testing.T) {
	root := t.TempDir()
	st, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetCurrentSession("telegram:1", "s1"); err != nil {
		t.Fatalf("SetCurrentSession: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	st, err = Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if got, ok := st.CurrentSession("telegram:1"); !ok || got != "s1" {
		t.Fatalf("CurrentSession after reopen = %q, %v; want s1, true", got, ok)
	}
}

func TestCurrentSession_PerSurface(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if err := st.SetCurrentSession("telegram:1", "sess-1"); err != nil {
		t.Fatalf("SetCurrentSession: %v", err)
	}
	if err := st.SetCurrentSession("telegram:2", "sess-2"); err != nil {
		t.Fatalf("SetCurrentSession: %v", err)
	}
	if err := st.SetCurrentSession("telegram:1", "sess-3"); err != nil {
		t.Fatalf("SetCurrentSession: %v", err)
	}
	if got, ok := st.CurrentSession("telegram:1"); !ok || got != "sess-3" {
		t.Errorf("CurrentSession(telegram:1) = %q, %v; want sess-3, true", got, ok)
	}
	if got, ok := st.CurrentSession("telegram:2"); !ok || got != "sess-2" {
		t.Errorf("CurrentSession(telegram:2) = %q, %v; want sess-2, true", got, ok)
	}
	if _, ok := st.CurrentSession("nope"); ok {
		t.Error("CurrentSession for an unrecorded surface should be absent")
	}
}
