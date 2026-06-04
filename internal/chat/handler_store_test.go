package chat

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/weatherjean/shell3/internal/store"
)

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "test-*.db")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	st, err := store.Open(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestStoreHandler_Name(t *testing.T) {
	for _, name := range []string{"history_get", "history_search"} {
		h := StoreHandler{toolName: name}
		if h.Name() != name {
			t.Errorf("Name() = %q, want %q", h.Name(), name)
		}
	}
}

func TestStoreHandler_NilStore(t *testing.T) {
	h := StoreHandler{toolName: "history_get"}
	out, _ := h.Execute(context.Background(), "1", json.RawMessage(`{}`), ToolConfig{Store: nil})
	if !strings.Contains(out, "store not available") {
		t.Fatalf("expected store-not-available error, got %q", out)
	}
}

func TestStoreHandler_HistoryGet_noHistory(t *testing.T) {
	st := openTestStore(t)
	h := StoreHandler{toolName: "history_get"}
	out, _ := h.Execute(context.Background(), "1", json.RawMessage(`{}`), ToolConfig{Store: st})
	if out != "No history found." {
		t.Fatalf("expected 'No history found.', got %q", out)
	}
}
