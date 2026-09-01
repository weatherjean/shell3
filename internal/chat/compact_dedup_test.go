package chat

import (
	"testing"

	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/runs"
)

func TestCompactInto_NoDuplicateMessages(t *testing.T) {
	st, err := runs.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open runs store: %v", err)
	}

	prevID, err := st.NewSession(runs.Meta{})
	if err != nil {
		t.Fatalf("new session: %v", err)
	}

	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "turn 1 user"},
		{Role: llm.RoleAssistant, Content: "turn 1 assistant"},
		{Role: llm.RoleUser, Content: "turn 2 user"},
	}

	for _, m := range msgs {
		if err := st.AppendMessage(prevID, m); err != nil {
			t.Fatalf("AppendMessage: %v", err)
		}
	}

	sess := NewSession(SessionOpts{StoreID: prevID})
	sess.messages = append(sess.messages, msgs...)
	sess.persistedLen = len(msgs) // high-water mark: all persisted

	compactInto(CompactSummary{Summary: "compacted"}, st, sess, nil, applog.Noop{}, "", "", "", "", "", "")

	got, err := st.LoadMessages(prevID)
	if err != nil {
		t.Fatalf("LoadMessages(prevID): %v", err)
	}
	if len(got) != len(msgs) {
		t.Errorf("outgoing session: got %d messages, want %d (duplication bug?)", len(got), len(msgs))
		for i, m := range got {
			t.Logf("  [%d] role=%s content=%q", i, m.Role, m.Content)
		}
	}
	for i := range msgs {
		if i >= len(got) {
			break
		}
		if got[i].Role != msgs[i].Role || got[i].Content != msgs[i].Content {
			t.Errorf("outgoing[%d]: got {%s %q}, want {%s %q}",
				i, got[i].Role, got[i].Content, msgs[i].Role, msgs[i].Content)
		}
	}
}
