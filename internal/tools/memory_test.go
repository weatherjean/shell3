package tools_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/weatherjean/shell3/internal/store"
	"github.com/weatherjean/shell3/internal/tools"
)

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "shell3.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestMemorySearchTool(t *testing.T) {
	st := openTestStore(t)
	st.MemoryStore("jwt", "use JWT with 1h expiry")

	tool := tools.NewMemorySearchTool(st)
	result, err := tool.Execute(context.Background(), map[string]any{"query": "JWT"})
	if err != nil {
		t.Fatal(err)
	}
	if result == "" {
		t.Error("expected non-empty result")
	}
}

func TestMemoryStoreTool(t *testing.T) {
	st := openTestStore(t)

	tool := tools.NewMemoryStoreTool(st)
	_, err := tool.Execute(context.Background(), map[string]any{
		"key":   "auth",
		"value": "JWT tokens",
	})
	if err != nil {
		t.Fatal(err)
	}

	results, _ := st.MemorySearch("JWT", 5)
	if len(results) == 0 {
		t.Error("expected stored entry to be searchable")
	}
}

func TestMemoryRemoveTool(t *testing.T) {
	st := openTestStore(t)
	st.MemoryStore("temp-key", "temp value")

	tool := tools.NewMemoryRemoveTool(st)
	_, err := tool.Execute(context.Background(), map[string]any{"key": "temp-key"})
	if err != nil {
		t.Fatal(err)
	}

	results, _ := st.MemorySearch("temp value", 5)
	if len(results) != 0 {
		t.Errorf("expected 0 results after remove, got %d", len(results))
	}
}

func TestMemoryListTool(t *testing.T) {
	st := openTestStore(t)
	st.MemoryStore("k1", "v1")
	st.MemoryStore("k2", "v2")

	tool := tools.NewMemoryListTool(st)
	result, err := tool.Execute(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if result == "" || result == "No memories stored." {
		t.Error("expected non-empty list")
	}
}

func TestHistoryLatestTool(t *testing.T) {
	st := openTestStore(t)
	sessionID, _ := st.StartSession()
	st.AppendHistory(sessionID, "user", "hello")

	tool := tools.NewHistoryLatestTool(st)
	result, err := tool.Execute(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if result == "" || result == "No history found." {
		t.Error("expected non-empty history")
	}
}

func TestHistorySearchTool(t *testing.T) {
	st := openTestStore(t)
	sessionID, _ := st.StartSession()
	st.AppendHistory(sessionID, "user", "how do I set up JWT authentication")
	st.AppendHistory(sessionID, "assistant", "use the jwt-go library")

	tool := tools.NewHistorySearchTool(st)
	result, err := tool.Execute(context.Background(), map[string]any{"query": "JWT authentication"})
	if err != nil {
		t.Fatal(err)
	}
	if result == "" || result == "No history found." {
		t.Error("expected non-empty history result")
	}
}
