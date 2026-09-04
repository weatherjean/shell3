package chat

import (
	"testing"

	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/runs"
)

func TestSaveHistory_AfterResume_DoesNotReflushSeed(t *testing.T) {
	st, err := runs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open runs store: %v", err)
	}
	id, err := st.NewSession()
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	seed := []llm.Message{
		{Role: llm.RoleUser, Content: "earlier question"},
		{Role: llm.RoleAssistant, Content: "earlier answer"},
	}
	for _, m := range seed {
		if err := st.AppendMessage(id, m); err != nil {
			t.Fatalf("AppendMessage: %v", err)
		}
	}

	sess := NewSession(SessionOpts{StoreID: id, Store: st, InitialMessages: seed})

	sess.messages = append(sess.messages,
		llm.Message{Role: llm.RoleUser, Content: "new question"},
		llm.Message{Role: llm.RoleAssistant, Content: "new answer"},
	)
	saveHistory(st, applog.Noop{}, sess, id)

	got, err := st.LoadMessages(id)
	if err != nil {
		t.Fatalf("LoadMessages: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("messages on disk after resumed turn: got %d, want 4 (seed must not be re-flushed)", len(got))
	}
}
