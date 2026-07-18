package chat

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/runs"
)

func TestFlushMessages_PersistsFullStreamIncludingToolResults(t *testing.T) {
	st, err := runs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	id, err := st.NewSession(runs.Meta{})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}

	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "hi"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "1", Name: "bash", RawArgs: `{}`}}},
		{Role: llm.RoleTool, ToolCallID: "1", Name: "bash", Content: "output"},
	}
	if n := flushMessages(st, applog.Noop{}, id, msgs); n != 3 {
		t.Fatalf("flushMessages persisted count = %d, want 3", n)
	}

	got, err := st.LoadMessages(id)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 3 || got[2].Role != llm.RoleTool || got[2].Content != "output" {
		t.Fatalf("tool result not persisted: %#v", got)
	}
}

// flushMessages must stop at the first append failure and report only the
// contiguous persisted prefix, so a caller never advances its high-water mark
// past an unwritten message (which would drop it permanently).
func TestFlushMessages_StopsAtFirstFailure(t *testing.T) {
	root := t.TempDir()
	st, err := runs.Open(root)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	id, err := st.NewSession(runs.Meta{})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	// Remove the session's run directory so every append fails (the file can't
	// be created in a missing directory).
	if err := os.RemoveAll(filepath.Join(root, "runs", id)); err != nil {
		t.Fatalf("remove run dir: %v", err)
	}
	msgs := []llm.Message{{Role: llm.RoleUser, Content: "a"}, {Role: llm.RoleUser, Content: "b"}}
	if n := flushMessages(st, applog.Noop{}, id, msgs); n != 0 {
		t.Fatalf("flushMessages persisted count = %d, want 0 (all appends fail)", n)
	}
}
