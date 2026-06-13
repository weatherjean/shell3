package chat

import (
	"testing"

	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/store"
)

func TestFlushMessages_PersistsFullStreamIncludingToolResults(t *testing.T) {
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()
	id, _ := st.StartSession("", "", "")

	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "hi"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "1", Name: "bash", RawArgs: `{}`}}},
		{Role: llm.RoleTool, ToolCallID: "1", Name: "bash", Content: "output"},
	}
	flushMessages(st, applog.Noop{}, id, 0, msgs)

	got, err := st.LoadSessionMessages(id)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != 3 || got[2].Role != llm.RoleTool || got[2].Content != "output" {
		t.Fatalf("tool result not persisted: %#v", got)
	}
}
