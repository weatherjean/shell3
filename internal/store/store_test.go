package store_test

import (
	"path/filepath"
	"testing"

	"github.com/weatherjean/shell3/internal/store"
)

func TestStore_Open(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "shell3.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
}

func TestStore_MemoryStoreAndSearch(t *testing.T) {
	st, _ := store.Open(filepath.Join(t.TempDir(), "shell3.db"))
	defer st.Close()

	if err := st.MemoryStore("auth-decision", "use JWT with 1h expiry"); err != nil {
		t.Fatal(err)
	}
	if err := st.MemoryStore("deploy-notes", "always run migrations before deploy"); err != nil {
		t.Fatal(err)
	}

	results, err := st.MemorySearch("JWT", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one result")
	}
	if results[0].Key != "auth-decision" {
		t.Errorf("got key %q, want auth-decision", results[0].Key)
	}
}

func TestStore_MemoryUpsert(t *testing.T) {
	st, _ := store.Open(filepath.Join(t.TempDir(), "shell3.db"))
	defer st.Close()

	st.MemoryStore("key1", "original")
	st.MemoryStore("key1", "updated")

	results, _ := st.MemorySearch("updated", 5)
	if len(results) != 1 {
		t.Fatalf("expected 1 result after upsert, got %d", len(results))
	}
}

func TestStore_MemoryDelete(t *testing.T) {
	st, _ := store.Open(filepath.Join(t.TempDir(), "shell3.db"))
	defer st.Close()

	st.MemoryStore("to-delete", "some value")
	if err := st.MemoryDelete("to-delete"); err != nil {
		t.Fatal(err)
	}

	results, _ := st.MemorySearch("some value", 5)
	if len(results) != 0 {
		t.Errorf("expected 0 results after delete, got %d", len(results))
	}
}

func TestStore_MemoryList(t *testing.T) {
	st, _ := store.Open(filepath.Join(t.TempDir(), "shell3.db"))
	defer st.Close()

	st.MemoryStore("key-a", "value a")
	st.MemoryStore("key-b", "value b")

	results, err := st.MemoryList(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
}

func TestStore_HistoryLatest(t *testing.T) {
	st, _ := store.Open(filepath.Join(t.TempDir(), "shell3.db"))
	defer st.Close()

	sessionID, _ := st.StartSession()
	st.AppendHistory(sessionID, "user", "first message")
	st.AppendHistory(sessionID, "assistant", "first reply")

	results, err := st.HistoryLatest(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
}

func TestStore_SessionLifecycle(t *testing.T) {
	st, _ := store.Open(filepath.Join(t.TempDir(), "shell3.db"))
	defer st.Close()

	id, err := st.StartSession()
	if err != nil {
		t.Fatal(err)
	}
	if id == 0 {
		t.Fatal("expected non-zero session id")
	}
	if err := st.EndSession(id); err != nil {
		t.Fatal(err)
	}
}

func TestStore_AppendAndSearchHistory(t *testing.T) {
	st, _ := store.Open(filepath.Join(t.TempDir(), "shell3.db"))
	defer st.Close()

	sessionID, _ := st.StartSession()
	st.AppendHistory(sessionID, "user", "how do I configure JWT expiry")
	st.AppendHistory(sessionID, "assistant", "set JWT_EXPIRY env var to 3600")

	results, err := st.SearchHistory("JWT", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one history result")
	}
	if results[0].SessionID != sessionID {
		t.Errorf("got session_id %d, want %d", results[0].SessionID, sessionID)
	}
}
