package memory_test

import (
	"path/filepath"
	"testing"

	"github.com/weatherjean/shell3/internal/memory"
)

func TestMemory_StoreAndSearch(t *testing.T) {
	db, err := memory.Open(filepath.Join(t.TempDir(), "mem.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := db.Store("auth-decision", "use JWT with 1h expiry"); err != nil {
		t.Fatal(err)
	}
	if err := db.Store("deploy-notes", "always run migrations before deploy"); err != nil {
		t.Fatal(err)
	}

	results, err := db.Search("JWT")
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

func TestMemory_Upsert(t *testing.T) {
	db, err := memory.Open(filepath.Join(t.TempDir(), "mem.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	db.Store("key1", "original value")
	db.Store("key1", "updated value")

	results, err := db.Search("updated")
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result after upsert, got %d", len(results))
	}
}
