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
	for _, name := range []string{"memory_upsert", "memory_list", "memory_search", "history_get", "history_search"} {
		h := StoreHandler{toolName: name}
		if h.Name() != name {
			t.Errorf("Name() = %q, want %q", h.Name(), name)
		}
	}
}

func TestStoreHandler_MemoryUpsertAndList(t *testing.T) {
	st := openTestStore(t)
	h := StoreHandler{toolName: "memory_upsert"}
	args := json.RawMessage(`{"key":"color","value":"blue"}`)
	out, err := h.Execute(context.Background(), "1", args, ToolConfig{Store: st})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "Stored") {
		t.Fatalf("unexpected upsert output: %q", out)
	}

	lh := StoreHandler{toolName: "memory_list"}
	out, _ = lh.Execute(context.Background(), "2", json.RawMessage(`{}`), ToolConfig{Store: st})
	if !strings.Contains(out, "color") || !strings.Contains(out, "blue") {
		t.Fatalf("memory_list missing stored entry: %q", out)
	}
}

func TestStoreHandler_MemorySearch(t *testing.T) {
	st := openTestStore(t)
	uh := StoreHandler{toolName: "memory_upsert"}
	_, _ = uh.Execute(context.Background(), "1", json.RawMessage(`{"key":"lang","value":"golang"}`), ToolConfig{Store: st})

	sh := StoreHandler{toolName: "memory_search"}
	args := json.RawMessage(`{"terms":["golang"]}`)
	out, _ := sh.Execute(context.Background(), "2", args, ToolConfig{Store: st})
	if !strings.Contains(out, "golang") {
		t.Fatalf("memory_search did not find entry: %q", out)
	}
}

func TestStoreHandler_NilStore(t *testing.T) {
	h := StoreHandler{toolName: "memory_list"}
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
