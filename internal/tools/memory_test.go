package tools_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/weatherjean/shell3/internal/memory"
	"github.com/weatherjean/shell3/internal/tools"
)

func TestMemorySearchTool(t *testing.T) {
	db, _ := memory.Open(filepath.Join(t.TempDir(), "m.db"))
	defer db.Close()
	db.Store("jwt", "use JWT with 1h expiry")

	tool := tools.NewMemorySearchTool(db)
	result, err := tool.Execute(context.Background(), map[string]any{"query": "JWT"})
	if err != nil {
		t.Fatal(err)
	}
	if result == "" {
		t.Error("expected non-empty result")
	}
}

func TestMemoryStoreTool(t *testing.T) {
	db, _ := memory.Open(filepath.Join(t.TempDir(), "m.db"))
	defer db.Close()

	tool := tools.NewMemoryStoreTool(db)
	_, err := tool.Execute(context.Background(), map[string]any{
		"key":   "auth",
		"value": "JWT tokens",
	})
	if err != nil {
		t.Fatal(err)
	}

	results, _ := db.Search("JWT")
	if len(results) == 0 {
		t.Error("expected stored entry to be searchable")
	}
}
