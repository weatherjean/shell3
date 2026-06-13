package chat

import (
	"testing"

	"github.com/weatherjean/shell3/internal/applog"
	"github.com/weatherjean/shell3/internal/llm"
	"github.com/weatherjean/shell3/internal/store"
)

func TestCompactInto_MirrorsCompactedContextToNewSession(t *testing.T) {
	st, _ := store.Open(":memory:")
	defer st.Close()
	id, _ := st.StartSession("", "", "")

	sess := NewSession(SessionOpts{StoreID: id})
	sess.messages = []llm.Message{
		{Role: llm.RoleUser, Content: "old 1"},
		{Role: llm.RoleAssistant, Content: "old 2"},
	}
	allMsgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "old 1"},
		{Role: llm.RoleAssistant, Content: "old 2"},
	}

	compactInto(CompactSummary{Summary: "did stuff"}, st, sess, allMsgs, applog.Noop{}, "", "", "")

	// New session id is now sess.id; its persisted messages must equal the
	// compacted in-memory list.
	got, err := st.LoadSessionMessages(sess.id)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(got) != len(sess.messages) {
		t.Fatalf("persisted %d msgs, in-memory %d", len(got), len(sess.messages))
	}
	for i := range got {
		if got[i].Role != sess.messages[i].Role || got[i].Content != sess.messages[i].Content {
			t.Fatalf("seq %d mismatch: %#v vs %#v", i, got[i], sess.messages[i])
		}
	}
}
