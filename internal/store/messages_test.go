package store

import (
	"reflect"
	"testing"

	"github.com/weatherjean/shell3/internal/llm"
)

func TestMessages_RoundTrip(t *testing.T) {
	st, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer st.Close()

	id, err := st.StartSession()
	if err != nil {
		t.Fatalf("start: %v", err)
	}

	want := []llm.Message{
		{Role: llm.RoleUser, Content: "list the files"},
		{Role: llm.RoleAssistant, Content: "", ToolCalls: []llm.ToolCall{
			{ID: "1", Name: "bash", RawArgs: `{"command":"ls"}`},
		}},
		{Role: llm.RoleTool, ToolCallID: "1", Name: "bash", Content: "a.go\nb.go\n"},
		{Role: llm.RoleAssistant, Content: "Two Go files."},
	}
	for i, m := range want {
		if err := st.AppendMessage(id, i, m); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}

	got, err := st.LoadSessionMessages(id)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("round-trip mismatch:\n got=%#v\nwant=%#v", got, want)
	}
}
