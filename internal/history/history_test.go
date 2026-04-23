package history_test

import (
	"path/filepath"
	"testing"

	"github.com/weatherjean/shell3/internal/history"
	"github.com/weatherjean/shell3/internal/llm"
)

func TestRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.md")

	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "hello"},
		{Role: llm.RoleAssistant, Content: "world"},
	}

	if err := history.Save(path, msgs); err != nil {
		t.Fatal(err)
	}

	loaded, err := history.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(loaded))
	}
	if loaded[0].Content != "hello" || loaded[1].Content != "world" {
		t.Errorf("unexpected messages: %+v", loaded)
	}
}

func TestLoad_Missing(t *testing.T) {
	msgs, err := history.Load("/nonexistent/path.md")
	if err != nil {
		t.Fatal("missing file should return empty, not error")
	}
	if len(msgs) != 0 {
		t.Errorf("expected empty, got %d messages", len(msgs))
	}
}
