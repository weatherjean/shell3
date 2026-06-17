package chat

import (
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
	flushMessages(st, applog.Noop{}, id, msgs)

	got, err := st.LoadMessages(id)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 3 || got[2].Role != llm.RoleTool || got[2].Content != "output" {
		t.Fatalf("tool result not persisted: %#v", got)
	}
}
